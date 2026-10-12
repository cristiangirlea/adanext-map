package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestVersionPrintsTheVersion(t *testing.T) {
	for _, arg := range []string{"version", "--version"} {
		var out, errOut bytes.Buffer
		if code := run([]string{arg}, &out, &errOut); code != 0 {
			t.Fatalf("%s: exit %d, stderr %q", arg, code, errOut.String())
		}
		if got, want := out.String(), "adamap "+Version+"\n"; got != want {
			t.Fatalf("%s: stdout %q, want %q", arg, got, want)
		}
		if errOut.Len() != 0 {
			t.Fatalf("%s: wrote to stderr: %q", arg, errOut.String())
		}
	}
}

func TestHelpGoesToStdout(t *testing.T) {
	var out, errOut bytes.Buffer
	if code := run([]string{"help"}, &out, &errOut); code != 0 {
		t.Fatalf("exit %d", code)
	}
	if !strings.HasPrefix(out.String(), "usage: adamap") || errOut.Len() != 0 {
		t.Fatalf("stdout %q, stderr %q", out.String(), errOut.String())
	}
}

func TestNoCommandAndUnknownCommandFailWithUsage(t *testing.T) {
	for _, args := range [][]string{nil, {"bogus"}} {
		var out, errOut bytes.Buffer
		if code := run(args, &out, &errOut); code != 2 {
			t.Errorf("%v: exit %d, want 2", args, code)
		}
		if out.Len() != 0 {
			t.Errorf("%v: wrote to stdout: %q", args, out.String())
		}
		if !strings.Contains(errOut.String(), "usage: adamap") {
			t.Errorf("%v: stderr %q lacks the usage", args, errOut.String())
		}
	}
	var out, errOut bytes.Buffer
	run([]string{"bogus"}, &out, &errOut)
	if !strings.Contains(errOut.String(), `unknown command "bogus"`) {
		t.Errorf("stderr %q does not name the command", errOut.String())
	}
}
