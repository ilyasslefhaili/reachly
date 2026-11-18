package smtpcheck

import (
	"bufio"
	"context"
	"net"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeSMTP is a tiny SMTP server. valid holds accepted mailboxes; catchAll
// accepts everything; greylist answers 451 the first time it sees an address.
type fakeSMTP struct {
	valid    map[string]bool
	catchAll bool
	greylist bool
	mu       sync.Mutex
	seen     map[string]bool
}

func (f *fakeSMTP) serve(t *testing.T) string {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	f.seen = map[string]bool{}
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go f.handle(c)
		}
	}()
	return ln.Addr().String()
}

func (f *fakeSMTP) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	w := func(s string) { c.Write([]byte(s + "\r\n")) }
	w("220 fake ESMTP")
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimSpace(line)
		up := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(up, "EHLO"):
			w("250-fake")
			w("250 SIZE 1000")
		case strings.HasPrefix(up, "MAIL FROM"):
			w("250 ok")
		case strings.HasPrefix(up, "RCPT TO"):
			addr := strings.ToLower(strings.Trim(line[len("RCPT TO:"):], "<> "))
			f.mu.Lock()
			first := !f.seen[addr]
			f.seen[addr] = true
			f.mu.Unlock()
			switch {
			case f.greylist && first:
				w("451 4.7.1 greylisted, try again")
			case f.catchAll || f.valid[addr]:
				w("250 2.1.5 ok")
			default:
				w("550 5.1.1 user unknown")
			}
		case strings.HasPrefix(up, "QUIT"):
			w("221 bye")
			return
		default:
			w("250 ok")
		}
	}
}

func newV(addr string) (*Verifier, string) {
	host, port, _ := net.SplitHostPort(addr)
	return &Verifier{HELO: "test", MailFrom: "a@test", Timeout: 2 * time.Second,
		GreylistWait: 50 * time.Millisecond, PerHost: 3, Port: port}, host
}

func TestVerifyValidAndInvalid(t *testing.T) {
	f := &fakeSMTP{valid: map[string]bool{"jean.dupont@acme.com": true}}
	v, host := newV(f.serve(t))
	res := v.Verify(context.Background(), []string{host}, "acme.com",
		[]string{"jean.dupont@acme.com", "jean@acme.com", "jdupont@acme.com"})
	if res.CatchAll != CatchAllNo {
		t.Errorf("catch-all = %s", res.CatchAll)
	}
	if res.Probes["jean.dupont@acme.com"].Status != Accepted {
		t.Error("expected accepted")
	}
	if res.Probes["jean@acme.com"].Status != Rejected || res.Probes["jdupont@acme.com"].Status != Rejected {
		t.Error("expected rejected")
	}
}

func TestVerifyCatchAll(t *testing.T) {
	f := &fakeSMTP{catchAll: true}
	v, host := newV(f.serve(t))
	res := v.Verify(context.Background(), []string{host}, "acme.com", []string{"x@acme.com"})
	if res.CatchAll != CatchAllYes {
		t.Errorf("catch-all = %s", res.CatchAll)
	}
}

func TestVerifyGreylistRetry(t *testing.T) {
	f := &fakeSMTP{greylist: true, valid: map[string]bool{"a@acme.com": true}}
	v, host := newV(f.serve(t))
	res := v.Verify(context.Background(), []string{host}, "acme.com", []string{"a@acme.com", "b@acme.com"})
	if res.Probes["a@acme.com"].Status != Accepted || res.Probes["b@acme.com"].Status != Rejected {
		t.Errorf("greylist retry failed: %+v", res.Probes)
	}
}

func TestClassify(t *testing.T) {
	if Classify(550, "5.7.1 Service unavailable; client host blocked using Spamhaus") != Unknown {
		t.Error("block message should be unknown")
	}
	if Classify(550, "5.1.1 The email account that you tried to reach does not exist") != Rejected {
		t.Error("should be rejected")
	}
}
