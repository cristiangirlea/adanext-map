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
