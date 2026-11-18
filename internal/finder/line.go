package finder

import (
	"errors"
	"strings"
)

var ErrBadLine = errors.New("use: full name, company or website")

// ParseLine splits a bulk input line "Full Name, company-or-website".
// The last comma, tab or semicolon is the separator, so URLs and names stay intact.
func ParseLine(line string) (name, company string, err error) {
	line = strings.TrimSpace(line)
	sep := strings.LastIndexAny(line, ",\t;")
	if sep < 0 {
		return line, "", ErrBadLine
	}
	name = strings.Trim(strings.TrimSpace(line[:sep]), `"`)
	company = strings.Trim(strings.TrimSpace(line[sep+1:]), `"`)
	if name == "" || company == "" {
		return name, company, ErrBadLine
	}
	return name, company, nil
}

// IsHeader reports whether a line looks like a CSV header ("name,company").
func IsHeader(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "name") && (strings.Contains(l, "company") || strings.Contains(l, "website") || strings.Contains(l, "domain"))
}
