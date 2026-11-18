// Package domain turns a company name or website into an email domain.
package domain

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/ilyasslefhaili/reachly/internal/mx"
)

// Resolver is the interface so a paid enrichment API can replace the heuristic later.
type Resolver interface {
	Resolve(ctx context.Context, companyOrWebsite string) (string, error)
}

type Heuristic struct {
	MX   *mx.Resolver
	HTTP *http.Client
}

func NewHeuristic(m *mx.Resolver) *Heuristic {
	return &Heuristic{MX: m, HTTP: &http.Client{
		Timeout:       5 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}}
}

// LooksLikeDomain reports whether s is a URL or a bare domain ("acme.com").
func LooksLikeDomain(s string) bool {
	s = strings.TrimSpace(s)
	return strings.Contains(s, "://") || (!strings.Contains(s, " ") && strings.Contains(s, "."))
}

// FromWebsite extracts "acme.com" from "https://www.acme.com/about".
func FromWebsite(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.Contains(s, "://") {
		s = "http://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", errors.New("could not read that website")
	}
	h := strings.ToLower(u.Hostname())
	h = strings.TrimPrefix(h, "www.")
	if !strings.Contains(h, ".") {
		return "", errors.New("could not read that website")
	}
	return h, nil
}

var suffixWords = map[string]bool{
	"inc": true, "llc": true, "ltd": true, "limited": true, "corp": true, "corporation": true,
	"co": true, "company": true, "gmbh": true, "sa": true, "sas": true, "sarl": true, "ag": true,
	"bv": true, "plc": true, "group": true, "holdings": true, "the": true,
}

func slug(company string) string {
	var words []string
	for _, w := range strings.Fields(strings.ToLower(company)) {
		w = strings.Trim(w, ".,&'\"")
		if w == "" || suffixWords[w] {
			continue
		}
		words = append(words, w)
	}
	var b strings.Builder
	for _, r := range strings.Join(words, "") {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// Candidates returns domain guesses in preference order.
func Candidates(company string) []string {
	s := slug(company)
	if s == "" {
		return nil
	}
	return []string{
		s + ".com", s + ".io", s + ".co", s + ".ai", s + ".net", s + ".org",
		s + "hq.com", "get" + s + ".com", "try" + s + ".com", s + "app.com", "join" + s + ".com",
	}
}

// Resolve accepts either a website or a company name. For a company name it
// checks every candidate domain in parallel (MX + HTTP) and returns the
// best-ranked one that passes.
func (h *Heuristic) Resolve(ctx context.Context, in string) (string, error) {
	if LooksLikeDomain(in) {
		return FromWebsite(in)
	}
	cands := Candidates(in)
	if len(cands) == 0 {
		return "", errors.New("enter a company name or website")
	}
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()

	type score struct{ mail, web bool }
	scores := make([]score, len(cands))
	var wg sync.WaitGroup
	for i, d := range cands {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var inner sync.WaitGroup
			inner.Add(2)
			go func() { defer inner.Done(); _, err := h.MX.Lookup(ctx, d); scores[i].mail = err == nil }()
			go func() { defer inner.Done(); scores[i].web = h.hasSite(ctx, d) }()
			inner.Wait()
		}()
	}
	wg.Wait()

	for i, s := range scores { // best: mail + website
		if s.mail && s.web {
			return cands[i], nil
		}
	}
	for i, s := range scores { // fallback: mail only
		if s.mail {
			return cands[i], nil
		}
	}
	return "", errors.New("could not find a domain for that company; try pasting its website")
}

func (h *Heuristic) hasSite(ctx context.Context, d string) bool {
	for _, scheme := range []string{"https://", "http://"} {
		req, _ := http.NewRequestWithContext(ctx, http.MethodHead, scheme+d, nil)
		if resp, err := h.HTTP.Do(req); err == nil {
			resp.Body.Close()
			if resp.StatusCode < 500 {
				return true
			}
		}
	}
	return false
}
