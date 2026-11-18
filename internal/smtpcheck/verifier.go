// Package smtpcheck verifies mailboxes by talking SMTP to the domain's mail
// server (any provider) up to RCPT TO. It never sends DATA, so no email is sent.
package smtpcheck

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"errors"
	"fmt"
	"math/big"
	"net"
	"net/textproto"
	"strconv"
	"strings"
	"sync"
	"time"
)

type Status string

const (
	Accepted Status = "accepted"
	Rejected Status = "rejected"
	TempFail Status = "tempfail"
	Unknown  Status = "unknown"
)

type Probe struct {
	Status  Status `json:"status"`
	Code    int    `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

type CatchAll string

const (
	CatchAllYes     CatchAll = "yes"
	CatchAllNo      CatchAll = "no"
	CatchAllUnknown CatchAll = "unknown"
)

type DomainResult struct {
	Host     string           `json:"host"` // MX host that answered
	CatchAll CatchAll         `json:"catch_all"`
	Probes   map[string]Probe `json:"probes"` // full address → result
	Note     string           `json:"note,omitempty"`
}

type Verifier struct {
	HELO         string
	MailFrom     string
	Timeout      time.Duration
	ProbeDelay   time.Duration
	GreylistWait time.Duration
	PerHost      int // max concurrent sessions per MX host
	Port         string

	mu   sync.Mutex
	sems map[string]chan struct{}
}

func (v *Verifier) sem(host string) chan struct{} {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.sems == nil {
		v.sems = map[string]chan struct{}{}
	}
	s, ok := v.sems[host]
	if !ok {
		n := v.PerHost
		if n < 1 {
			n = 1
		}
		s = make(chan struct{}, n)
		v.sems[host] = s
	}
	return s
}

// Verify checks every address in parallel: one goroutine runs the catch-all
// probe while the candidates are split across up to PerHost-1 other sessions.
// Addresses that get a 4xx (greylisting) are retried once after GreylistWait.
func (v *Verifier) Verify(ctx context.Context, hosts []string, domain string, addrs []string) DomainResult {
	res := DomainResult{CatchAll: CatchAllUnknown, Probes: map[string]Probe{}}
	host, err := v.pickHost(ctx, hosts)
	if err != nil {
		res.Note = "could not connect to any mail server on port 25: " + err.Error()
		for _, a := range addrs {
			res.Probes[a] = Probe{Status: Unknown, Message: "connection failed"}
		}
		return res
	}
	res.Host = host

	var (
		mu sync.Mutex
		wg sync.WaitGroup
	)
	merge := func(m map[string]Probe) {
		mu.Lock()
		for k, p := range m {
			res.Probes[k] = p
		}
		mu.Unlock()
	}

	// Catch-all probe with a random mailbox nobody would have.
	fake := randomLocal() + "@" + domain
	wg.Add(1)
	go func() {
		defer wg.Done()
		m, note := v.sessionWithRetry(ctx, host, []string{fake})
		mu.Lock()
		defer mu.Unlock()
		switch m[fake].Status {
		case Accepted:
			res.CatchAll = CatchAllYes
		case Rejected:
			res.CatchAll = CatchAllNo
		}
		if note != "" && res.Note == "" {
			res.Note = note
		}
	}()

	chunks := v.PerHost - 1
	if chunks < 1 {
		chunks = 1
	}
	for _, part := range split(addrs, chunks) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m, note := v.sessionWithRetry(ctx, host, part)
			merge(m)
			if note != "" {
				mu.Lock()
				if res.Note == "" {
					res.Note = note
				}
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	return res
}

func (v *Verifier) sessionWithRetry(ctx context.Context, host string, rcpts []string) (map[string]Probe, string) {
	from := v.MailFrom
	out, note := v.session(ctx, host, rcpts, from)
	if senderRejected(out) && from != "" {
		// Server dislikes our sender domain: retry at once with the null sender <>.
		from = ""
		out, note = v.session(ctx, host, rcpts, from)
		if senderRejected(out) {
			note = "the mail server rejected the sender address; set REACHLY_MAIL_FROM to an address on a real domain you own"
		}
	}
	var retry []string
	for _, r := range rcpts {
		if out[r].Status == TempFail {
			retry = append(retry, r)
		}
	}
	if len(retry) > 0 {
		select {
		case <-ctx.Done():
		case <-time.After(v.GreylistWait):
			again, n2 := v.session(ctx, host, retry, from)
			for k, p := range again {
				out[k] = p
			}
			if n2 != "" {
				note = n2
			}
		}
	}
	for k, p := range out {
		if p.Status == TempFail { // still deferred after retry
			p.Status = Unknown
			out[k] = p
		}
	}
	return out, note
}

func (v *Verifier) port() string {
	if v.Port != "" {
		return v.Port
	}
	return "25"
}

// pickHost returns the first MX (by priority) that accepts a TCP connection.
func (v *Verifier) pickHost(ctx context.Context, hosts []string) (string, error) {
	var last error = errors.New("no hosts")
	for _, h := range hosts {
		d := net.Dialer{Timeout: v.Timeout}
		c, err := d.DialContext(ctx, "tcp", net.JoinHostPort(h, v.port()))
		if err == nil {
			c.Close()
			return h, nil
		}
		last = err
	}
	return "", last
}

// session runs one SMTP conversation: EHLO, STARTTLS, MAIL FROM, then one
// RCPT TO per address in a single transaction, then QUIT.
func (v *Verifier) session(ctx context.Context, host string, rcpts []string, from string) (map[string]Probe, string) {
	out := map[string]Probe{}
	fail := func(msg string) (map[string]Probe, string) {
		for _, r := range rcpts {
			if _, done := out[r]; !done {
				out[r] = Probe{Status: Unknown, Message: msg}
			}
		}
		return out, msg
	}

	sem := v.sem(host)
	select {
	case sem <- struct{}{}:
		defer func() { <-sem }()
	case <-ctx.Done():
		return fail("cancelled")
	}

	d := net.Dialer{Timeout: v.Timeout}
	raw, err := d.DialContext(ctx, "tcp", net.JoinHostPort(host, v.port()))
	if err != nil {
		return fail("connect: " + err.Error())
	}
	conn := raw
	defer func() { conn.Close() }()
	stop := context.AfterFunc(ctx, func() { conn.SetDeadline(time.Now()) })
	defer stop()

	tp := textproto.NewConn(conn)
	cmd := func(format string, args ...any) (int, string, error) {
		conn.SetDeadline(time.Now().Add(v.Timeout))
		if format != "" {
			if err := tp.PrintfLine(format, args...); err != nil {
				return 0, "", err
			}
		}
		return tp.ReadResponse(0)
	}

	code, msg, err := cmd("") // greeting
	if err != nil || code != 220 {
		return fail(fmt.Sprintf("bad greeting %d %s", code, msg))
	}
	code, msg, err = cmd("EHLO %s", v.HELO)
	if err != nil {
		return fail("EHLO: " + err.Error())
	}
	if code != 250 {
		if code, msg, err = cmd("HELO %s", v.HELO); err != nil || code != 250 {
			return fail(fmt.Sprintf("HELO rejected %d %s", code, msg))
		}
	}
	if strings.Contains(strings.ToUpper(msg), "STARTTLS") {
		if c, _, err := cmd("STARTTLS"); err == nil && c == 220 {
			// Verification only: many MX certificates do not match their hostname.
			tc := tls.Client(raw, &tls.Config{ServerName: host, InsecureSkipVerify: true})
			tc.SetDeadline(time.Now().Add(v.Timeout))
			if err := tc.Handshake(); err != nil {
				return fail("TLS: " + err.Error())
			}
			conn = tc
			tp = textproto.NewConn(tc)
			if c, m, err := cmd("EHLO %s", v.HELO); err != nil || c != 250 {
				return fail(fmt.Sprintf("EHLO after TLS %d %s", c, m))
			}
		}
	}
	code, msg, err = cmd("MAIL FROM:<%s>", from)
	if err != nil {
		return fail("MAIL FROM: " + err.Error())
	}
	if code != 250 {
		return fail(fmt.Sprintf("server refused sender (%d %s), your IP may be blocklisted", code, oneLine(msg)))
	}
	for i, r := range rcpts {
		if i > 0 && v.ProbeDelay > 0 {
			select {
			case <-time.After(v.ProbeDelay):
			case <-ctx.Done():
				return fail("cancelled")
			}
		}
		code, msg, err = cmd("RCPT TO:<%s>", r)
		if err != nil {
			return fail("connection dropped: " + err.Error())
		}
		out[r] = Probe{Status: Classify(code, msg), Code: code, Message: oneLine(msg)}
		if code == 421 { // server closing the connection
			return fail("server closed connection (421)")
		}
	}
	cmd("QUIT")
	return out, ""
}

var blockWords = []string{"block", "spamhaus", "blacklist", "blocklist", "denied", "policy", "reputation", "rbl", "not permitted", "spf", "helo"}
var userWords = []string{"user", "mailbox", "recipient", "address", "no such", "unknown", "not found", "does not exist", "invalid", "rejected"}

// Classify maps an RCPT response to a status. A 5xx that talks about blocking
// or reputation (not the mailbox) is Unknown, not Rejected.
func Classify(code int, msg string) Status {
	m := strings.ToLower(msg)
	switch {
	case code == 250 || code == 251:
		return Accepted
	case code >= 400 && code < 500:
		return TempFail
	case code >= 500 && code < 600:
		hasUser := containsAny(m, userWords)
		if containsAny(m, blockWords) && !(hasUser && strings.Contains(m, "5.1.1")) {
			return Unknown
		}
		if code == 550 || code == 551 || code == 553 || hasUser {
			return Rejected
		}
		return Unknown
	}
	return Unknown
}

// senderRejected reports whether any reply blames our MAIL FROM address.
func senderRejected(m map[string]Probe) bool {
	for _, p := range m {
		if p.Status != Accepted && strings.Contains(strings.ToLower(p.Message), "sender address rejected") {
			return true
		}
	}
	return false
}

func containsAny(s string, words []string) bool {
	for _, w := range words {
		if strings.Contains(s, w) {
			return true
		}
	}
	return false
}

func oneLine(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) > 200 {
		s = s[:200] + "…"
	}
	return s
}

func split(xs []string, n int) [][]string {
	if len(xs) == 0 {
		return nil
	}
	if n > len(xs) {
		n = len(xs)
	}
	out := make([][]string, n)
	for i, x := range xs { // round-robin keeps the top-ranked patterns spread across sessions
		out[i%n] = append(out[i%n], x)
	}
	return out
}

func randomLocal() string {
	const letters = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, 14)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(letters))))
		b[i] = letters[n.Int64()]
	}
	return "zq" + string(b) + strconv.Itoa(int(time.Now().Unix()%1000))
}
