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

func TestCheckPathRefusesACharacterAFileNameCannotHold(t *testing.T) {
	for _, p := range []string{"a.go:x", "dir/a:x.go", "a:b", "a\x00b.go", "a\nb.go", "a\tb.go", "a\x1fb.go",
		"a\x7fb.go", "a\u0085b.go", "pkg/\x00"} {
		if err := CheckPath(p); err == nil {
			t.Errorf("CheckPath(%q) accepted it", p)
		}
	}
}

func TestCheckPathAcceptsOrdinaryPaths(t *testing.T) {
	for _, p := range []string{"a.go", "pkg/a.go", `pkg\a.go`, "a b.go", "ünï/çode.go", "a-b_c.d.e", ".hidden"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q) = %v, want it accepted", p, err)
		}
	}
}

func TestCheckPathRefusesAWindowsDeviceName(t *testing.T) {
	for _, p := range []string{"con", "nul", "prn", "aux", "com0", "com1", "com9", "lpt0", "lpt1", "lpt9",
		"con.txt", "aux.go", "NUL.tar.gz", "Com1.Go", "CON", "NuL", "pkg/nul.go", `pkg\nul.go`, "a/con/b.go",
		"nul.", "nul..", "nul ", "nul. .", "con .txt", "con. .go", "aux.",
		"com\u00b9", "COM\u00b2.go", "com\u00b3", "lpt\u00b9", "LPT\u00b2.txt", "lpt\u00b3",
		"conin$", "CONOUT$", "ConIn$.txt", "pkg/conout$.go", "conin$."} {
		if err := CheckPath(p); err == nil {
			t.Errorf("CheckPath(%q) accepted it", p)
		}
	}
}

func TestCheckPathAcceptsANameThatOnlyLooksLikeADevice(t *testing.T) {
	for _, p := range []string{"console.go", "nullable.go", "com10.go", "auxiliary/x.go", "com.go", "lpt.go",
		"prn1.go", "com\u00b90.go", "con-1.go", "nul x.go", ".nul", "x.con", "xcon", "com1x",
		"lpt10", "ccon.go", "nu.l", "conin", "conin$x", "conout.go", "xconin$"} {
		if err := CheckPath(p); err != nil {
			t.Errorf("CheckPath(%q) = %v, want it accepted", p, err)
		}
	}
}
