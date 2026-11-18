// Package names turns a free-form full name into email-safe name parts.
package names

import (
	"errors"
	"strings"
	"unicode"
)

type Name struct {
	First   string   // "jean"
	Middles []string // middle names, if any
	Last    string   // "dupont" (hyphenated last names are joined: "garciamarquez")
	LastRaw []string // last-name pieces before joining: ["garcia","marquez"]
}

var translit = map[rune]string{
	'à': "a", 'á': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a", 'ā': "a",
	'æ': "ae", 'ç': "c", 'č': "c", 'ć': "c",
	'è': "e", 'é': "e", 'ê': "e", 'ë': "e", 'ē': "e", 'ę': "e", 'ě': "e",
	'ì': "i", 'í': "i", 'î': "i", 'ï': "i", 'ī': "i", 'ı': "i",
	'ñ': "n", 'ń': "n", 'ň': "n",
	'ò': "o", 'ó': "o", 'ô': "o", 'õ': "o", 'ö': "o", 'ø': "o", 'ō': "o", 'ő': "o",
	'œ': "oe", 'ß': "ss", 'ś': "s", 'š': "s", 'ş': "s",
	'ù': "u", 'ú': "u", 'û': "u", 'ü': "u", 'ū': "u", 'ů': "u", 'ű': "u",
	'ý': "y", 'ÿ': "y", 'ž': "z", 'ź': "z", 'ż': "z", 'ł': "l", 'ř': "r", 'ğ': "g", 'đ': "d", 'ð': "d", 'þ': "th",
}

// honorifics and suffixes dropped from names.
var drop = map[string]bool{
	"mr": true, "mrs": true, "ms": true, "miss": true, "dr": true, "prof": true, "sir": true,
	"jr": true, "sr": true, "ii": true, "iii": true, "iv": true, "phd": true, "md": true, "mba": true,
}

// ascii lowercases s and transliterates accents, keeping only a-z.
func ascii(s string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(s) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case translit[r] != "":
			b.WriteString(translit[r])
		}
	}
	return b.String()
}

// Parse splits a full name. Handles "Last, First", accents, hyphens, apostrophes
// ("O'Brien" → "obrien"), honorifics and suffixes.
func Parse(full string) (Name, error) {
	full = strings.TrimSpace(full)
	if i := strings.Index(full, ","); i > 0 {
		// "Dupont, Jean" → "Jean Dupont"
		full = strings.TrimSpace(full[i+1:]) + " " + strings.TrimSpace(full[:i])
	}
	fields := strings.FieldsFunc(full, func(r rune) bool { return unicode.IsSpace(r) })
	var parts [][]string // each token may contain hyphen pieces
	for _, f := range fields {
		if drop[ascii(f)] {
			continue
		}
		var pieces []string
		for _, p := range strings.Split(f, "-") {
			if a := ascii(p); a != "" {
				pieces = append(pieces, a)
			}
		}
		if len(pieces) > 0 {
			parts = append(parts, pieces)
		}
	}
	if len(parts) < 2 {
		return Name{}, errors.New("please enter a first and last name")
	}
	n := Name{First: strings.Join(parts[0], "")}
	last := parts[len(parts)-1]
	n.LastRaw = last
	n.Last = strings.Join(last, "")
	for _, m := range parts[1 : len(parts)-1] {
		n.Middles = append(n.Middles, strings.Join(m, ""))
	}
	return n, nil
}
