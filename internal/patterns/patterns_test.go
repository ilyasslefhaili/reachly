package patterns

import (
	"testing"

	"github.com/ilyasslefhaili/reachly/internal/names"
)

func TestGenerate(t *testing.T) {
	n, _ := names.Parse("Jean Dupont")
	c := Generate(n)
	if c[0].Local != "jean.dupont" || c[1].Local != "jean" || c[2].Local != "jdupont" {
		t.Fatalf("unexpected order: %+v", c[:3])
	}
	seen := map[string]bool{}
	for i, x := range c {
		if seen[x.Local] {
			t.Errorf("duplicate %q", x.Local)
		}
		seen[x.Local] = true
		if x.Rank != i {
			t.Errorf("rank mismatch at %d", i)
		}
	}
}
