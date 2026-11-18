package names

import "testing"

func TestParse(t *testing.T) {
	cases := []struct {
		in, first, last string
	}{
		{"Jean Dupont", "jean", "dupont"},
		{"  José  García-Márquez ", "jose", "garciamarquez"},
		{"Dr. Sean O'Brien Jr.", "sean", "obrien"},
		{"Dupont, Jean", "jean", "dupont"},
		{"Anne Marie Lefèvre", "anne", "lefevre"},
		{"Jean-Luc Picard", "jeanluc", "picard"},
	}
	for _, c := range cases {
		n, err := Parse(c.in)
		if err != nil {
			t.Fatalf("%q: %v", c.in, err)
		}
		if n.First != c.first || n.Last != c.last {
			t.Errorf("%q: got %q %q, want %q %q", c.in, n.First, n.Last, c.first, c.last)
		}
	}
	if _, err := Parse("Madonna"); err == nil {
		t.Error("single name should error")
	}
}
