// Package finder runs one lookup: name → domain → MX → patterns → SMTP → score.
package finder

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/ilyasslefhaili/reachly/internal/domain"
	"github.com/ilyasslefhaili/reachly/internal/mx"
	"github.com/ilyasslefhaili/reachly/internal/names"
	"github.com/ilyasslefhaili/reachly/internal/patterns"
	"github.com/ilyasslefhaili/reachly/internal/smtpcheck"
)

type Status string

const (
	Valid    Status = "valid"
	Invalid  Status = "invalid"
	CatchAll Status = "catch_all"
	Unknown  Status = "unknown"
)

type Candidate struct {
	Email      string `json:"email"`
	Pattern    string `json:"pattern"`
	Status     Status `json:"status"`
	Confidence int    `json:"confidence"`
	SMTPCode   int    `json:"smtp_code,omitempty"`
	SMTPMsg    string `json:"smtp_message,omitempty"`
}

type Result struct {
	Name       string      `json:"name"`
	Domain     string      `json:"domain"`
	MXHost     string      `json:"mx_host"`
	Provider   string      `json:"provider"`
	CatchAll   string      `json:"catch_all"`
	Best       *Candidate  `json:"best,omitempty"`
	Candidates []Candidate `json:"candidates"`
	Note       string      `json:"note,omitempty"`
	DurationMS int64       `json:"duration_ms"`
}

type Finder struct {
	Domains  domain.Resolver
	MX       *mx.Resolver
	Verifier *smtpcheck.Verifier

	mu       sync.Mutex
	catchAll map[string]cached // domain → known catch-all state
	results  map[string]cachedResult
}

type cached struct {
	v   smtpcheck.CatchAll
	exp time.Time
}
type cachedResult struct {
	r   Result
	exp time.Time
}

const cacheTTL = time.Hour

func New(d domain.Resolver, m *mx.Resolver, v *smtpcheck.Verifier) *Finder {
	return &Finder{Domains: d, MX: m, Verifier: v, catchAll: map[string]cached{}, results: map[string]cachedResult{}}
}

func (f *Finder) Find(ctx context.Context, fullName, company string) (Result, error) {
	start := time.Now()
	n, err := names.Parse(fullName)
	if err != nil {
		return Result{}, err
	}
	dom, err := f.Domains.Resolve(ctx, company)
	if err != nil {
		return Result{}, err
	}
	key := n.First + "|" + strings.Join(n.Middles, " ") + "|" + n.Last + "@" + dom
	f.mu.Lock()
	if c, ok := f.results[key]; ok && time.Now().Before(c.exp) {
		f.mu.Unlock()
		return c.r, nil
	}
	f.mu.Unlock()

	rec, err := f.MX.Lookup(ctx, dom)
	if err != nil {
		return Result{}, err
	}
	cands := patterns.Generate(n)
	res := Result{Name: fullName, Domain: dom, Provider: rec.Provider, MXHost: rec.Hosts[0]}

	// Known catch-all domain: SMTP would accept every guess, so skip probing.
	f.mu.Lock()
	ca, known := f.catchAll[dom]
	f.mu.Unlock()
	var dr smtpcheck.DomainResult
	if known && time.Now().Before(ca.exp) && ca.v == smtpcheck.CatchAllYes {
		dr = smtpcheck.DomainResult{Host: rec.Hosts[0], CatchAll: smtpcheck.CatchAllYes, Probes: map[string]smtpcheck.Probe{}}
		for _, c := range cands {
			dr.Probes[c.Local+"@"+dom] = smtpcheck.Probe{Status: smtpcheck.Accepted, Message: "domain is catch-all (cached)"}
		}
	} else {
		addrs := make([]string, len(cands))
		for i, c := range cands {
			addrs[i] = c.Local + "@" + dom
		}
		dr = f.Verifier.Verify(ctx, rec.Hosts, dom, addrs)
		if dr.CatchAll != smtpcheck.CatchAllUnknown {
			f.mu.Lock()
			f.catchAll[dom] = cached{dr.CatchAll, time.Now().Add(cacheTTL)}
			f.mu.Unlock()
		}
	}
	if dr.Host != "" {
		res.MXHost = dr.Host
	}
	res.CatchAll = string(dr.CatchAll)
	res.Note = dr.Note
	res.Candidates = score(cands, dom, dr)
	res.Best = pickBest(res.Candidates)
	res.DurationMS = time.Since(start).Milliseconds()

	if ctx.Err() == nil && dr.Note == "" {
		f.mu.Lock()
		f.results[key] = cachedResult{res, time.Now().Add(cacheTTL)}
		f.mu.Unlock()
	}
	return res, nil
}

func score(cands []patterns.Candidate, dom string, dr smtpcheck.DomainResult) []Candidate {
	out := make([]Candidate, 0, len(cands))
	for _, c := range cands {
		email := c.Local + "@" + dom
		p := dr.Probes[email]
		cand := Candidate{Email: email, Pattern: c.Pattern, SMTPCode: p.Code, SMTPMsg: p.Message}
		switch p.Status {
		case smtpcheck.Accepted:
			switch dr.CatchAll {
			case smtpcheck.CatchAllNo:
				cand.Status, cand.Confidence = Valid, clamp(97-c.Rank, 85, 99)
			case smtpcheck.CatchAllYes:
				cand.Status, cand.Confidence = CatchAll, clamp(60-c.Rank*4, 10, 60)
			default: // accepted but the catch-all probe did not answer
				cand.Status, cand.Confidence = Valid, clamp(75-c.Rank*2, 50, 75)
			}
		case smtpcheck.Rejected:
			cand.Status, cand.Confidence = Invalid, 0
		default:
			cand.Status, cand.Confidence = Unknown, clamp(30-c.Rank*2, 5, 30)
		}
		out = append(out, cand)
	}
	return out
}

// pickBest: the top-ranked valid address; otherwise the top-ranked catch-all
// or unknown guess; nil when every guess was rejected.
func pickBest(cs []Candidate) *Candidate {
	for _, want := range []Status{Valid, CatchAll, Unknown} {
		for i := range cs {
			if cs[i].Status == want {
				c := cs[i]
				return &c
			}
		}
	}
	return nil
}

func clamp(v, lo, hi int) int {
	return max(lo, min(hi, v))
}
