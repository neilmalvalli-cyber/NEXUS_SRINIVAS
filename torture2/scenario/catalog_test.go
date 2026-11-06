package scenario

import (
	"fmt"
	"testing"
)

// The catalog is the official list: 32 scenarios, groups A-F with the
// official sizes, stable IDs, and every one with an invariant and a method.
func TestCatalogIsComplete(t *testing.T) {
	want := map[string]int{"A": 7, "B": 6, "C": 6, "D": 4, "E": 5, "F": 4}
	got := map[string]int{}
	seen := map[string]bool{}
	cat := Catalog()
	if len(cat) != 32 {
		t.Fatalf("%d scenarios, want 32", len(cat))
	}
	for _, s := range cat {
		got[s.Group]++
		if seen[s.ID] {
			t.Errorf("duplicate ID %s", s.ID)
		}
		seen[s.ID] = true
		if s.ID != fmt.Sprintf("%s%d", s.Group, got[s.Group]) {
			t.Errorf("ID %s out of order in group %s", s.ID, s.Group)
		}
		if s.Title == "" || s.Invariant == "" || s.Method == "" || s.run == nil {
			t.Errorf("%s is missing a title, invariant, method or body", s.ID)
		}
	}
	for g, n := range want {
		if got[g] != n {
			t.Errorf("group %s has %d scenarios, want %d", g, got[g], n)
		}
	}
}
