// Package patterns generates candidate email local parts, ranked by how
// common each corporate email format is.
package patterns

import "github.com/ilyasslefhaili/reachly/internal/names"

type Candidate struct {
	Pattern string // human-readable pattern, e.g. "first.last"
	Local   string // local part, e.g. "jean.dupont"
	Rank    int    // 0 = most common
}

// Generate returns de-duplicated candidates in rank order.
func Generate(n names.Name) []Candidate {
	f, l := n.First, n.Last
	fi, li := f[:1], l[:1]
	list := []struct{ p, v string }{
		{"first.last", f + "." + l},
		{"first", f},
		{"flast", fi + l},
		{"firstlast", f + l},
		{"first_last", f + "_" + l},
		{"firstl", f + li},
		{"f.last", fi + "." + l},
		{"first.l", f + "." + li},
		{"last.first", l + "." + f},
		{"lastfirst", l + f},
		{"last", l},
		{"lastf", l + fi},
		{"first-last", f + "-" + l},
		{"fl", fi + li},
		{"last_first", l + "_" + f},
	}
	if len(n.Middles) > 0 {
		mi := n.Middles[0][:1]
		list = append(list,
			struct{ p, v string }{"fmlast", fi + mi + l},
			struct{ p, v string }{"first.m.last", f + "." + mi + "." + l},
		)
	}
	if len(n.LastRaw) > 1 {
		// hyphenated last name: also try "first.garcia-marquez" and first piece only
		hy := n.LastRaw[0]
		for _, p := range n.LastRaw[1:] {
			hy += "-" + p
		}
		list = append(list,
			struct{ p, v string }{"first.last-hyphen", f + "." + hy},
			struct{ p, v string }{"first.lastpart1", f + "." + n.LastRaw[0]},
		)
	}
	seen := map[string]bool{}
	var out []Candidate
	for _, c := range list {
		if seen[c.v] {
			continue
		}
		seen[c.v] = true
		out = append(out, Candidate{Pattern: c.p, Local: c.v, Rank: len(out)})
	}
	return out
}
