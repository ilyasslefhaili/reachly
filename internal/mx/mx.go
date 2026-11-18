// Package mx finds the mail servers for a domain (any provider) and labels
// which provider runs them.
package mx

import (
	"context"
	"errors"
	"net"
	"sort"
	"strings"
	"sync"
	"time"
)

type Record struct {
	Hosts    []string `json:"hosts"`    // MX hosts by priority
	Provider string   `json:"provider"` // label only, never gates a check
}

var ErrNoMail = errors.New("domain has no mail server (no MX or A record)")

type Resolver struct {
	r   *net.Resolver
	ttl time.Duration
	mu  sync.Mutex
	c   map[string]entry
}

type entry struct {
	rec Record
	err error
	exp time.Time
}

func New() *Resolver {
	return &Resolver{r: net.DefaultResolver, ttl: 30 * time.Minute, c: map[string]entry{}}
}

// Lookup returns MX hosts for domain, falling back to the domain itself when
// it has only an A/AAAA record (RFC 5321 §5.1). Results are cached.
func (x *Resolver) Lookup(ctx context.Context, domain string) (Record, error) {
	domain = strings.ToLower(strings.TrimSuffix(domain, "."))
	x.mu.Lock()
	if e, ok := x.c[domain]; ok && time.Now().Before(e.exp) {
		x.mu.Unlock()
		return e.rec, e.err
	}
	x.mu.Unlock()

	rec, err := x.lookup(ctx, domain)
	// Do not cache context cancellations.
	if ctx.Err() == nil {
		x.mu.Lock()
		x.c[domain] = entry{rec, err, time.Now().Add(x.ttl)}
		x.mu.Unlock()
	}
	return rec, err
}

func (x *Resolver) lookup(ctx context.Context, domain string) (Record, error) {
	mxs, err := x.r.LookupMX(ctx, domain)
	if err == nil && len(mxs) > 0 {
		sort.SliceStable(mxs, func(i, j int) bool { return mxs[i].Pref < mxs[j].Pref })
		var hosts []string
		for _, m := range mxs {
			h := strings.TrimSuffix(m.Host, ".")
			if h == "" { // "null MX" (RFC 7505): domain accepts no mail
				return Record{}, ErrNoMail
			}
			hosts = append(hosts, h)
		}
		return Record{Hosts: hosts, Provider: Fingerprint(hosts[0])}, nil
	}
	if addrs, aerr := x.r.LookupHost(ctx, domain); aerr == nil && len(addrs) > 0 {
		return Record{Hosts: []string{domain}, Provider: "self-hosted/other"}, nil
	}
	return Record{}, ErrNoMail
}

var providers = []struct{ suffix, name string }{
	{"google.com", "Google Workspace"},
	{"googlemail.com", "Google Workspace"},
	{"mail.protection.outlook.com", "Microsoft 365"},
	{"outlook.com", "Microsoft 365"},
	{"zoho.com", "Zoho Mail"}, {"zoho.eu", "Zoho Mail"}, {"zoho.in", "Zoho Mail"},
	{"yahoodns.net", "Yahoo"},
	{"secureserver.net", "GoDaddy"},
	{"ovh.net", "OVH"},
	{"ionos.com", "IONOS"}, {"1and1.com", "IONOS"}, {"kundenserver.de", "IONOS"},
	{"protonmail.ch", "Proton Mail"},
	{"messagingengine.com", "Fastmail"},
	{"icloud.com", "iCloud"},
	{"yandex.net", "Yandex"},
	{"mailgun.org", "Mailgun"},
	{"pphosted.com", "Proofpoint (gateway)"},
	{"ppe-hosted.com", "Proofpoint (gateway)"},
	{"mimecast.com", "Mimecast (gateway)"},
	{"barracudanetworks.com", "Barracuda (gateway)"},
	{"iphmx.com", "Cisco IronPort (gateway)"},
	{"messagelabs.com", "Broadcom/Symantec (gateway)"},
	{"trendmicro.com", "Trend Micro (gateway)"},
	{"sophos.com", "Sophos (gateway)"},
}

// Fingerprint labels an MX host by provider; unknown hosts are "self-hosted/other".
func Fingerprint(host string) string {
	h := strings.ToLower(host)
	for _, p := range providers {
		if h == p.suffix || strings.HasSuffix(h, "."+p.suffix) {
			return p.name
		}
	}
	return "self-hosted/other"
}
