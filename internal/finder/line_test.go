package finder

import "testing"

func TestParseLine(t *testing.T) {
	ok := map[string][2]string{
		"Jean Dupont, acme.com":            {"Jean Dupont", "acme.com"},
		"Dupont, Jean, https://acme.com/x": {"Dupont, Jean", "https://acme.com/x"},
		"Jane Doe\tGlobex Inc":             {"Jane Doe", "Globex Inc"},
		`"Sam Lee";"initech.io"`:           {"Sam Lee", "initech.io"},
	}
	for in, want := range ok {
		n, c, err := ParseLine(in)
		if err != nil || n != want[0] || c != want[1] {
			t.Errorf("%q → %q %q %v", in, n, c, err)
		}
	}
	for _, bad := range []string{"no separator", "Jean Dupont,", ", acme.com"} {
		if _, _, err := ParseLine(bad); err == nil {
			t.Errorf("%q should fail", bad)
		}
	}
	if !IsHeader("name,company") || IsHeader("Jean Dupont, acme.com") {
		t.Error("IsHeader")
	}
}
