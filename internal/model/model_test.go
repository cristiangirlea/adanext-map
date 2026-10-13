package model

import "testing"

func TestCountsIncludeLocalsAndEveryDocumentsOccurrences(t *testing.T) {
	x := &Index{
		Symbols: []Symbol{{ID: "a"}, {ID: "b"}},
		Documents: []Document{
			{Path: "a.go", Locals: []Symbol{{ID: "local 0"}}, Occurrences: make([]Occurrence, 3)},
			{Path: "b.go", Occurrences: make([]Occurrence, 2)},
		},
	}
	if got, want := x.Counts(), (Counts{Documents: 2, Symbols: 3, Occurrences: 5}); got != want {
		t.Fatalf("Counts() = %+v, want %+v", got, want)
	}
}

func TestRoleHasEveryRoleAsked(t *testing.T) {
	r := RoleReference | RoleRead
	if !r.Has(RoleRead) || !r.Has(RoleReference|RoleRead) {
		t.Errorf("%b lacks a role it has", r)
	}
	if r.Has(RoleDefinition) || r.Has(RoleRead|RoleWrite) {
		t.Errorf("%b has a role it lacks", r)
	}
}

func TestLimitsCheckNamesTheFirstCountOverItsLimit(t *testing.T) {
	l := Limits{Documents: 2, Symbols: 3, Occurrences: 4, Relationships: 5}
	if err := l.Check(Counts{Documents: 2, Symbols: 3, Occurrences: 4}, 5); err != nil {
		t.Fatalf("counts at the limits: %v", err)
	}
	for _, tc := range []struct {
		c    Counts
		rels int
		want string
	}{
		{Counts{Documents: 3}, 0, "more than the limit of 2 documents"},
		{Counts{Symbols: 4}, 0, "more than the limit of 3 symbols"},
		{Counts{Occurrences: 5}, 0, "more than the limit of 4 occurrences"},
		{Counts{}, 6, "more than the limit of 5 relationships"},
	} {
		if err := l.Check(tc.c, tc.rels); err == nil || err.Error() != tc.want {
			t.Errorf("Check(%+v, %d) = %v, want %q", tc.c, tc.rels, err, tc.want)
		}
	}
}

func TestDefaultLimitsFitALargeMonorepo(t *testing.T) {
	// A Rust workspace of 2,000 files measured about 20,000 occurrences per 17 files: 2.4 million.
	l := DefaultLimits()
	if err := l.Check(Counts{Documents: 2000, Symbols: 600_000, Occurrences: 2_400_000}, 600_000); err != nil {
		t.Fatalf("a 2,000 file Rust workspace is over the default limits: %v", err)
	}
}
