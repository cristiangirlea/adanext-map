package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

func writeManifest(t *testing.T, m *manifest.Manifest) string {
	t.Helper()
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(t.TempDir(), "manifest.json")
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func call(env []string, args ...string) (int, string, string) {
	var out, errOut bytes.Buffer
	code := run(args, env, &out, &errOut)
	return code, out.String(), errOut.String()
}

func TestValidateSaysValidOrNamesEachIssue(t *testing.T) {
	code, out, errOut := call(nil, "validate", "--manifest", writeManifest(t, benchtest.Synthetic()))
	if code != 0 || !strings.Contains(out, "valid") || !strings.Contains(out, "SYNTHETIC") {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
	m := benchtest.Pilot()
	m.Backends[0].Version = "latest"
	m.Tasks[0].Checks = nil
	code, out, _ = call(nil, "validate", "--manifest", writeManifest(t, m))
	if code != 1 || !strings.Contains(out, "backends[0].version") || !strings.Contains(out, "blocks a run") ||
		!strings.Contains(out, "tasks[0].checks") || !strings.Contains(out, "structural") {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestValidateChecksRecordsAgainstTheManifest(t *testing.T) {
	m := benchtest.Synthetic()
	dir := t.TempDir()
	recs := benchtest.Records(m)
	recs[3].Source.Commit = benchtest.Rev(77)
	for i := range recs {
		if err := record.Write(dir, &recs[i]); err != nil {
			t.Fatal(err)
		}
	}
	code, out, _ := call(nil, "validate", "--manifest", writeManifest(t, m), "--records", dir)
	if code != 1 || !strings.Contains(out, "source drift") || !strings.Contains(out, recs[3].RunID) {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
}

func TestDryRunPrintsThePlanWithoutApproval(t *testing.T) {
	code, out, errOut := call([]string{"ANTHROPIC_API_KEY=sk-secret"}, "run", "--manifest", writeManifest(t, benchtest.Pilot()), "--dry-run")
	if code != 0 || !strings.Contains(out, "150 planned runs") || !strings.Contains(out, "ANTHROPIC_API_KEY") || strings.Contains(out, "sk-secret") {
		t.Fatalf("exit %d, stdout %.400q, stderr %q", code, out, errOut)
	}
}

func TestRunRefusesWithoutApprovalAndWritesNothing(t *testing.T) {
	records := filepath.Join(t.TempDir(), "records")
	work := filepath.Join(t.TempDir(), "work")
	mp := writeManifest(t, benchtest.Synthetic())
	for _, args := range [][]string{
		{"run", "--manifest", mp, "--records", records, "--work", work},
		{"run", "--manifest", mp, "--records", records, "--work", work, "--i-have-approval-for-N-runs", "149"},
	} {
		code, _, errOut := call(nil, args...)
		if code != 1 || !strings.Contains(errOut, "--i-have-approval-for-N-runs 150") {
			t.Fatalf("%v: exit %d, stderr %q", args, code, errOut)
		}
	}
	code, _, errOut := call([]string{"OPENAI_API_KEY=x"}, "run", "--manifest", mp, "--records", records, "--work", work, "--i-have-approval-for-N-runs", "150")
	if code != 1 || !strings.Contains(errOut, "OPENAI_API_KEY") {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	for _, d := range []string{records, work} {
		if _, err := os.Stat(d); err == nil {
			t.Fatalf("%s was created by a refused run", d)
		}
	}
	if code, _, errOut := call(nil, "run", "--manifest", mp, "--i-have-approval-for-N-runs", "150"); code != 2 || !strings.Contains(errOut, "--records") {
		t.Fatalf("no records dir: exit %d, stderr %q", code, errOut)
	}
}

func TestReduceRendersAndKeepsSyntheticOutOfRESULTS(t *testing.T) {
	m := benchtest.Synthetic()
	dir := t.TempDir()
	recs := benchtest.Records(m)
	for i := range recs {
		if err := record.Write(dir, &recs[i]); err != nil {
			t.Fatal(err)
		}
	}
	mp := writeManifest(t, m)
	code, out, errOut := call(nil, "reduce", "--manifest", mp, "--records", dir)
	if code != 0 || !strings.HasPrefix(out, "# Benchmark results") || !strings.Contains(out, "SYNTHETIC") || !strings.Contains(out, "--manifest "+filepath.ToSlash(mp)) {
		t.Fatalf("exit %d, stdout %.300q, stderr %q", code, out, errOut)
	}
	results := filepath.Join(t.TempDir(), "RESULTS.md")
	if code, _, errOut := call(nil, "reduce", "--manifest", mp, "--records", dir, "--out", results); code != 1 || !strings.Contains(errOut, "synthetic") {
		t.Fatalf("synthetic into RESULTS.md: exit %d, stderr %q", code, errOut)
	}
	if _, err := os.Stat(results); err == nil {
		t.Fatal("RESULTS.md was written from synthetic records")
	}
	other := filepath.Join(t.TempDir(), "synthetic.md")
	if code, _, errOut := call(nil, "reduce", "--manifest", mp, "--records", dir, "--out", other); code != 0 {
		t.Fatalf("exit %d, stderr %q", code, errOut)
	}
	recs[0].Checks[0].Output = "tampered"
	if err := record.Write(dir, &recs[0]); err != nil {
		t.Fatal(err)
	}
	if code, _, errOut := call(nil, "reduce", "--manifest", mp, "--records", dir); code != 1 || !strings.Contains(errOut, "output hash") {
		t.Fatalf("tampered records: exit %d, stderr %q", code, errOut)
	}
}

func TestFakeAgentAnswersWithThePrompt(t *testing.T) {
	var out bytes.Buffer
	code := fakeAgent(strings.NewReader("the prompt"), &out, []string{"ADAMAP_BENCH_ATTEMPT=1"})
	if code != 0 || !strings.Contains(out.String(), `"result":"the prompt"`) || !strings.Contains(out.String(), `"synthetic":true`) {
		t.Fatalf("exit %d, %s", code, out.String())
	}
	out.Reset()
	if code := fakeAgent(strings.NewReader("x"), &out, []string{"ADAMAP_BENCH_ATTEMPT=1", "ADAMAP_BENCH_FAKE_FAIL=1"}); code == 0 {
		t.Fatal("a failing attempt exits 0")
	}
}

func TestUsageAndUnknownCommands(t *testing.T) {
	if code, out, _ := call(nil, "help"); code != 0 || !strings.Contains(out, "usage: adamap-bench") {
		t.Fatalf("help: %d %q", code, out)
	}
	if code, _, errOut := call(nil, "bogus"); code != 2 || !strings.Contains(errOut, `unknown command "bogus"`) {
		t.Fatalf("bogus: %d %q", code, errOut)
	}
	if code, _, _ := call(nil); code != 2 {
		t.Fatalf("no command: %d", code)
	}
}
