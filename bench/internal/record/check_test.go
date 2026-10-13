package record_test

import (
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/bench/internal/benchtest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

func TestAValidSyntheticSetHasNoProblems(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	if len(recs) != m.PlannedRuns() {
		t.Fatalf("%d records", len(recs))
	}
	if p := record.Check(m, recs); len(p) != 0 {
		t.Fatalf("%d problems, first %v", len(p), p[0])
	}
}

// Each case breaks one record of a valid synthetic set and names the problem Check must report.
func TestCheckRejects(t *testing.T) {
	const nav = "t00.none.fake.r1"       // a navigation task, no index
	const fix = "t02.adamap.fake.r1"     // a fix task, the index built
	const reuse = "t02.adamap.fake.r2"   // the same cell, the index reused
	const control = "t03.serena.fake.r1" // answer and no-changes checks
	cases := []struct {
		name   string
		mutate func(recs []record.Record) []record.Record
		run    string
		msg    string
	}{
		// IDs.
		{"a duplicate run id", func(r []record.Record) []record.Record { return append(r, *benchtest.Find(r, nav)) }, nav, "duplicate run id"},
		{"swapped task and condition", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			x.Task, x.Condition = x.Condition, x.Task
			return r
		}, nav, "swapped"},
		{"an id the plan does not hold", func(r []record.Record) []record.Record { benchtest.Find(r, nav).RunID = "t00.none.fake.r9"; return r }, "t00.none.fake.r9", "not a planned run"},
		{"fields that are not its run's", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Repeat = 2; return r }, nav, "planned as"},
		{"a missing run id", func(r []record.Record) []record.Record { benchtest.Find(r, nav).RunID = ""; return r }, "", "no run id"},
		{"a real record among synthetic ones", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Synthetic = false; return r }, nav, "synthetic and real records never mix"},
		{"another manifest's record", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Manifest = "other"; return r }, nav, `manifest "other"`},
		// Source and index drift.
		{"a workspace at another commit", func(r []record.Record) []record.Record {
			benchtest.Find(r, nav).Source.Commit = benchtest.Rev(99)
			return r
		}, nav, "source drift"},
		{"a dirty workspace at the right commit", func(r []record.Record) []record.Record {
			benchtest.Find(r, nav).Source.Content = record.SHA256("edited")
			return r
		}, nav, "a commit alone does not identify a dirty tree"},
		{"an index of another commit", func(r []record.Record) []record.Record {
			benchtest.Find(r, fix).Index.Commit = benchtest.Rev(98)
			return r
		}, fix, "index drift"},
		{"an index of a dirty tree", func(r []record.Record) []record.Record {
			benchtest.Find(r, fix).Index.Content = record.SHA256("x")
			return r
		}, fix, "index drift"},
		{"another digest in a warm cell", func(r []record.Record) []record.Record {
			benchtest.Find(r, reuse).Index.Digest = record.SHA256("y")
			return r
		}, reuse, "index drift: digest"},
		{"no index where the condition has one", func(r []record.Record) []record.Record { benchtest.Find(r, fix).Index = nil; return r }, fix, "no index"},
		{"an index where the condition has none", func(r []record.Record) []record.Record {
			benchtest.Find(r, nav).Index = &record.Index{Digest: "d"}
			return r
		}, nav, "has no index"},
		{"a build without its time", func(r []record.Record) []record.Record { benchtest.Find(r, fix).Index.BuildMS = nil; return r }, fix, "build time"},
		{"a planned reuse that built", func(r []record.Record) []record.Record { benchtest.Find(r, reuse).Index.Reused = false; return r }, reuse, "planned to reuse"},
		// Checks.
		{"a declared check missing", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, control)
			x.Checks = x.Checks[:1]
			return r
		}, control, `check "untouched" missing`},
		{"an undeclared check", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			x.Checks = append(x.Checks, record.CheckResult{ID: "extra", Type: "command"})
			return r
		}, nav, `check "extra" not declared`},
		{"a check of another type", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Checks[0].Type = "command"; return r }, nav, "declared as answer"},
		{"an output that does not match its hash", func(r []record.Record) []record.Record {
			benchtest.Find(r, fix).Checks[0].Output += "tampered"
			return r
		}, fix, "output hash"},
		{"passed on a failing exit", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, fix)
			x.Checks[0].Exit = benchtest.Int(1)
			return r
		}, fix, "says passed=true, its output says false"},
		{"a command check without its exit", func(r []record.Record) []record.Record { benchtest.Find(r, fix).Checks[0].Exit = nil; return r }, fix, "says passed=true, its output says false"},
		{"an answer check passed without the answer", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			x.Answer = "pkg/a.go only"
			x.AnswerSHA256 = record.SHA256(x.Answer)
			x.Checks[0].Output = x.Answer
			x.Checks[0].OutputSHA256 = record.SHA256(x.Answer)
			return r
		}, nav, "says passed=true, its output says false"},
		{"an answer check on another text", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			x.Checks[0].Output = "pkg/a.go pkg/b.go and more"
			x.Checks[0].OutputSHA256 = record.SHA256(x.Checks[0].Output)
			return r
		}, nav, "not the run's answer"},
		{"an answer that does not match its hash", func(r []record.Record) []record.Record { benchtest.Find(r, nav).AnswerSHA256 = "0"; return r }, nav, "answer hash"},
		{"a check on another tree", func(r []record.Record) []record.Record {
			benchtest.Find(r, fix).Checks[0].Content = record.CleanContent
			return r
		}, fix, "ran on content"},
		{"no-changes passed on a changed tree", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, control)
			x.After.Content = record.SHA256("changed")
			for i := range x.Checks {
				x.Checks[i].Content = x.After.Content
			}
			return r
		}, control, `"untouched" says passed=true`},
		{"fix-tests without its overlay", func(r []record.Record) []record.Record { benchtest.Find(r, fix).Checks[0].Overlay = ""; return r }, fix, "overlay"},
		// Attempts and usage.
		{"no attempts", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Attempts = nil; return r }, nav, "no attempts"},
		{"more attempts than the policy", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			a := x.Attempts[0]
			x.Attempts = []record.Attempt{a, a, a}
			for i := range x.Attempts {
				x.Attempts[i].N = i + 1
			}
			return r
		}, nav, "3 attempts, the policy allows 2"},
		{"attempts out of sequence", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Attempts[0].N = 2; return r }, nav, "attempt 1 is numbered 2"},
		{"a completed attempt retried", func(r []record.Record) []record.Record {
			x := benchtest.Find(r, nav)
			second := x.Attempts[0]
			second.N = 2
			x.Attempts = append(x.Attempts, second)
			return r
		}, nav, "attempt 1 completed but was retried"},
		{"incomplete counters", func(r []record.Record) []record.Record {
			benchtest.Find(r, nav).Attempts[0].Usage.Output = nil
			return r
		}, nav, "incomplete counters: output missing"},
		{"usage in the wrong semantics", func(r []record.Record) []record.Record {
			benchtest.Find(r, nav).Attempts[0].Usage.Semantics = record.InputIncludesCache
			return r
		}, nav, "semantics"},
		{"unchecked isolation", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Isolation.Checked = false; return r }, nav, "isolation was not checked"},
		{"an unsupported run with attempts", func(r []record.Record) []record.Record { benchtest.Find(r, nav).Unsupported = "no LSP"; return r }, nav, "unsupported run has attempts"},
	}
	for _, c := range cases {
		m := benchtest.Synthetic()
		recs := c.mutate(benchtest.Records(m))
		problems := record.Check(m, recs)
		found := false
		for _, p := range problems {
			if p.RunID == c.run && strings.Contains(p.Msg, c.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no problem for %q saying %q; got %v", c.name, c.run, c.msg, problems)
		}
	}
}

func TestCheckRefusesSyntheticRecordsUnderARealManifest(t *testing.T) {
	recs := benchtest.Records(benchtest.Synthetic())
	m := benchtest.Pilot()
	problems := record.Check(m, recs)
	if len(problems) == 0 || !strings.Contains(problems[0].Msg, "synthetic") {
		t.Fatalf("problems %v", problems)
	}
}

func TestUsageArithmetic(t *testing.T) {
	excl := record.Usage{Source: record.Reported, Semantics: record.InputExcludesCache,
		Input: benchtest.Int64(100), CacheCreation: benchtest.Int64(20), CacheRead: benchtest.Int64(1000), Output: benchtest.Int64(50)}
	tok, ok := excl.Tokens()
	if !ok || tok != (record.Tokens{Fresh: 100, CacheCreation: 20, CacheRead: 1000, Output: 50, Total: 1170}) {
		t.Fatalf("excludes: %+v %v", tok, ok)
	}
	// Codex: input 1100 already holds the 1000 cached; the total is 1150, not 2150.
	incl := record.Usage{Source: record.Reported, Semantics: record.InputIncludesCache,
		Input: benchtest.Int64(1100), CacheRead: benchtest.Int64(1000), Output: benchtest.Int64(50)}
	tok, ok = incl.Tokens()
	if !ok || tok != (record.Tokens{Fresh: 100, CacheRead: 1000, Output: 50, Total: 1150}) {
		t.Fatalf("includes: %+v %v", tok, ok)
	}
	if p := incl.Problems(record.InputIncludesCache); len(p) != 0 {
		t.Fatalf("valid includes usage: %v", p)
	}

	cases := []struct {
		name string
		u    record.Usage
		sem  string
		msg  string
	}{
		{"cached input added twice", record.Usage{Source: record.Reported, Semantics: record.InputIncludesCache,
			Input: benchtest.Int64(1100), CacheRead: benchtest.Int64(1000), Output: benchtest.Int64(50), Total: benchtest.Int64(2150)}, record.InputIncludesCache, "adds cached input twice"},
		{"a total that disagrees", record.Usage{Source: record.Reported, Semantics: record.InputExcludesCache,
			Input: benchtest.Int64(100), CacheCreation: benchtest.Int64(0), CacheRead: benchtest.Int64(10), Output: benchtest.Int64(5), Total: benchtest.Int64(105)}, record.InputExcludesCache, "total 105, the counters give 115"},
		{"cache reads above the input that holds them", record.Usage{Source: record.Reported, Semantics: record.InputIncludesCache,
			Input: benchtest.Int64(100), CacheRead: benchtest.Int64(1000), Output: benchtest.Int64(5)}, record.InputIncludesCache, "cache reads 1000 exceed input 100"},
		{"cache creation where the provider has none", record.Usage{Source: record.Reported, Semantics: record.InputIncludesCache,
			Input: benchtest.Int64(100), CacheCreation: benchtest.Int64(5), CacheRead: benchtest.Int64(10), Output: benchtest.Int64(5)}, record.InputIncludesCache, "no cache-creation counter"},
		{"a missing counter", record.Usage{Source: record.Reported, Semantics: record.InputExcludesCache,
			Input: benchtest.Int64(100), CacheRead: benchtest.Int64(10), Output: benchtest.Int64(5)}, record.InputExcludesCache, "incomplete counters: cache_creation missing"},
		{"a negative counter", record.Usage{Source: record.Reported, Semantics: record.InputExcludesCache,
			Input: benchtest.Int64(-1), CacheCreation: benchtest.Int64(0), CacheRead: benchtest.Int64(10), Output: benchtest.Int64(5)}, record.InputExcludesCache, "negative"},
		{"unavailable with counters", record.Usage{Source: record.Unavailable, Input: benchtest.Int64(0)}, record.InputExcludesCache, "unavailable usage carries counters"},
		{"an estimate without its estimator", record.Usage{Source: record.Estimated, Total: benchtest.Int64(10)}, record.InputExcludesCache, "estimator"},
		{"an unknown source", record.Usage{Source: "guessed"}, record.InputExcludesCache, "source"},
	}
	for _, c := range cases {
		found := false
		for _, p := range c.u.Problems(c.sem) {
			if strings.Contains(p, c.msg) {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no problem saying %q; got %v", c.name, c.msg, c.u.Problems(c.sem))
		}
	}

	// Missing is not zero: unavailable and estimated usage give no reported tokens.
	for _, u := range []record.Usage{{Source: record.Unavailable}, {Source: record.Estimated, Estimator: "bytes/4", Total: benchtest.Int64(10)},
		{Source: record.Reported, Semantics: record.InputExcludesCache, Input: benchtest.Int64(1)}} {
		if _, ok := u.Tokens(); ok {
			t.Errorf("%+v gives reported tokens", u)
		}
	}
}

func TestSuccessComesFromTheChecksNotAFlag(t *testing.T) {
	m := benchtest.Synthetic()
	recs := benchtest.Records(m)
	x := benchtest.Find(recs, "t02.adamap.fake.r1")
	if !record.Success(m, x) {
		t.Fatal("a passing fix run is not a success")
	}
	x.Checks[0].Exit = benchtest.Int(1)
	x.Checks[0].Passed = false
	if record.Success(m, x) {
		t.Fatal("a failing test is a success")
	}
	x.Checks[0].Exit = benchtest.Int(0)
	x.Checks[0].Passed = true
	x.Attempts[0].TimedOut = true
	if record.Success(m, x) {
		t.Fatal("a timed-out attempt is a success")
	}
	x.Attempts[0].TimedOut = false
	x.Isolation.Violations = []string{"read outside the workspace"}
	if record.Success(m, x) {
		t.Fatal("an isolation failure is a success")
	}
	x.Isolation.Violations = nil
	x.Unsupported = "no LSP"
	if record.Success(m, x) {
		t.Fatal("an unsupported run is a success")
	}
}

func TestContentHashIgnoresOrder(t *testing.T) {
	a := []record.Entry{{" M", "a", "1"}, {"??", "b", "2"}}
	b := []record.Entry{a[1], a[0]}
	if record.ContentHash(a) != record.ContentHash(b) || record.ContentHash(a) == record.CleanContent {
		t.Fatal("content hash depends on order, or a dirty tree hashes as clean")
	}
}
