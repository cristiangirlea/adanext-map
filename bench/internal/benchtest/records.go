package benchtest

import (
	"fmt"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
	"github.com/cristiangirlea/adanext-map/bench/internal/record"
)

// Int64 is a pointer to n, for usage counters.
func Int64(n int64) *int64 { return &n }

// Int is a pointer to n.
func Int(n int) *int { return &n }

// Scale multiplies a condition's synthetic tokens and wall time, so the reducer has differences to find.
var Scale = map[string]float64{"none": 1.0, "lsp": 0.95, "serena": 0.9, "codegraph": 0.92, "adamap": 0.8}

// Records is one valid synthetic record for every run of m's plan: every check passes, the source
// is clean at the task's revision, warm cells share one index, usage is reported in the
// backend's semantics. m must be synthetic; the records say so.
func Records(m *manifest.Manifest) []record.Record {
	tasks := map[string]manifest.Task{}
	for _, t := range m.Tasks {
		tasks[t.ID] = t
	}
	conds := map[string]manifest.Condition{}
	for _, c := range m.Conditions {
		conds[c.ID] = c
	}
	var out []record.Record
	for i, run := range m.Plan() {
		t := tasks[run.Task]
		c := conds[run.Condition]
		scale := Scale[run.Condition]
		if scale == 0 {
			scale = 1
		}
		base := int64(20000 + 1000*(i%7))
		r := record.Record{
			Format: record.Format, RunID: run.ID, Task: run.Task, Condition: run.Condition, Backend: run.Backend,
			Repeat: run.Repeat, Synthetic: m.Synthetic, Manifest: m.Name,
			Source:    record.Fingerprint{Commit: t.Revision, Content: record.CleanContent},
			After:     record.Fingerprint{Commit: t.Revision, Content: record.CleanContent},
			Isolation: record.Isolation{Checked: true},
		}
		if t.Kind == manifest.TaskFix {
			r.After.Content = record.ContentHash([]record.Entry{{Status: " M", Path: "pkg/a.go", SHA256: record.SHA256(run.ID)}})
		}
		if c.Index != nil {
			r.Index = &record.Index{Digest: record.SHA256(run.Task + "/" + run.Condition + "/" + run.Backend),
				Commit: t.Revision, Content: record.CleanContent, Reused: run.Index == "reuse", Bytes: 4096}
			if run.Index == "build" {
				r.Index.BuildMS = Int64(30000)
			}
		}
		r.Attempts = []record.Attempt{{
			N: 1, WallMS: int64(float64(60000+1000*(i%5)) * scale), Exit: 0,
			Usage: record.Usage{Source: record.Reported, Semantics: record.InputExcludesCache,
				Input: Int64(int64(float64(base) * scale)), CacheCreation: Int64(1000), CacheRead: Int64(int64(float64(4*base) * scale)), Output: Int64(800)},
			Cost:     record.Cost{Source: record.Unavailable},
			Observed: record.Observed{Observable: true, ToolCalls: map[string]int{"Read": 3}, SourceReads: Int(3), SourceBytes: Int64(9000)},
		}}
		var answer []string
		for _, ch := range t.Checks {
			if ch.Type == manifest.CheckAnswer {
				answer = append(answer, ch.Expect.Contains...)
			}
		}
		r.Answer = strings.Join(answer, "\n")
		r.AnswerSHA256 = record.SHA256(r.Answer)
		for _, ch := range t.Checks {
			res := record.CheckResult{ID: ch.ID, Type: ch.Type, Content: r.After.Content, Passed: true}
			switch ch.Type {
			case manifest.CheckCommand, manifest.CheckFixTests:
				res.Exit = Int(0)
				res.Output = fmt.Sprintf("ok  \t%s\t0.1s\n", run.ID)
				if ch.Type == manifest.CheckFixTests {
					res.Overlay = t.Provenance.FixRevision
				}
			case manifest.CheckAnswer:
				res.Output = r.Answer
			case manifest.CheckReview:
				res.Passed = false
			}
			res.OutputSHA256 = record.SHA256(res.Output)
			r.Checks = append(r.Checks, res)
		}
		out = append(out, r)
	}
	return out
}

// Find is the record with the given run ID in recs.
func Find(recs []record.Record, id string) *record.Record {
	for i := range recs {
		if recs[i].RunID == id {
			return &recs[i]
		}
	}
	panic("no record " + id)
}
