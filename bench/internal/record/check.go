package record

import (
	"fmt"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/bench/internal/manifest"
)

// Problem is one thing wrong with a record set.
type Problem struct {
	RunID string
	Msg   string
}

func (p Problem) String() string { return p.RunID + ": " + p.Msg }

// Check holds every record to the manifest and its plan: the run IDs, the source and index the
// run says it used, its attempts and usage, and its checks recomputed from their outputs. A record
// set that passes Check is what the reducer may read.
func Check(m *manifest.Manifest, recs []Record) []Problem {
	c := checker{m: m, plan: map[string]manifest.Run{}, tasks: map[string]manifest.Task{},
		conds: map[string]manifest.Condition{}, backends: map[string]manifest.Backend{}}
	for _, r := range m.Plan() {
		c.plan[r.ID] = r
	}
	for _, t := range m.Tasks {
		c.tasks[t.ID] = t
	}
	for _, x := range m.Conditions {
		c.conds[x.ID] = x
	}
	for _, b := range m.Backends {
		c.backends[b.ID] = b
	}
	seen := map[string]bool{}
	digests := map[string]string{} // warm cell -> the digest its first record named
	for i := range recs {
		r := &recs[i]
		if r.RunID == "" {
			c.add("", fmt.Sprintf("record %d has no run id", i))
			continue
		}
		if seen[r.RunID] {
			c.add(r.RunID, "duplicate run id")
			continue
		}
		seen[r.RunID] = true
		c.record(r, digests)
	}
	return c.problems
}

type checker struct {
	m        *manifest.Manifest
	plan     map[string]manifest.Run
	tasks    map[string]manifest.Task
	conds    map[string]manifest.Condition
	backends map[string]manifest.Backend
	problems []Problem
}

func (c *checker) add(run, msg string) { c.problems = append(c.problems, Problem{run, msg}) }

func (c *checker) record(r *Record, digests map[string]string) {
	id := r.RunID
	if r.Format != Format {
		c.add(id, fmt.Sprintf("format %d, want %d", r.Format, Format))
	}
	if r.Synthetic != c.m.Synthetic {
		c.add(id, fmt.Sprintf("synthetic=%v under a manifest with synthetic=%v: synthetic and real records never mix", r.Synthetic, c.m.Synthetic))
		return
	}
	if r.Manifest != c.m.Name {
		c.add(id, fmt.Sprintf("manifest %q, want %q", r.Manifest, c.m.Name))
	}
	run, ok := c.plan[id]
	if !ok {
		c.add(id, "not a planned run")
		return
	}
	if r.Task != run.Task || r.Condition != run.Condition || r.Backend != run.Backend || r.Repeat != run.Repeat {
		msg := fmt.Sprintf("planned as task %q, condition %q, backend %q, repeat %d; the record says %q, %q, %q, %d",
			run.Task, run.Condition, run.Backend, run.Repeat, r.Task, r.Condition, r.Backend, r.Repeat)
		if r.Task == run.Condition || r.Condition == run.Task || r.Backend == run.Task || r.Task == run.Backend {
			msg = "swapped ids: " + msg
		}
		c.add(id, msg)
		return
	}
	task, cond, backend := c.tasks[run.Task], c.conds[run.Condition], c.backends[run.Backend]

	if r.Source.Commit != task.Revision {
		c.add(id, fmt.Sprintf("source drift: the workspace was at %s, the task's revision is %s", r.Source.Commit, task.Revision))
	}
	if r.Source.Content != CleanContent {
		c.add(id, "source drift: the workspace differed from its commit before the worker started; a commit alone does not identify a dirty tree")
	}
	if r.After.Commit != r.Source.Commit {
		c.add(id, fmt.Sprintf("the checks ran at commit %s, the run started at %s", r.After.Commit, r.Source.Commit))
	}
	if r.Unsupported != "" {
		if len(r.Attempts) > 0 || len(r.Checks) > 0 {
			c.add(id, "an unsupported run has attempts or checks")
		}
		return
	}
	c.index(r, run, cond, digests)
	c.attempts(r, backend)
	c.checks(r, task)
	if !r.Isolation.Checked {
		c.add(id, "isolation was not checked")
	}
	for i, o := range r.Overhead {
		for _, p := range o.Usage.Problems(SemanticsFor(backend.CLI)) {
			c.add(id, fmt.Sprintf("overhead %d (%s): %s", i, o.What, p))
		}
	}
}

func (c *checker) index(r *Record, run manifest.Run, cond manifest.Condition, digests map[string]string) {
	id := r.RunID
	x := r.Index
	switch {
	case cond.Index == nil && x != nil:
		c.add(id, fmt.Sprintf("condition %q has no index, the record has one", cond.ID))
		return
	case cond.Index == nil:
		return
	case x == nil:
		c.add(id, fmt.Sprintf("no index: condition %q prepares one before every run", cond.ID))
		return
	}
	if x.Digest == "" {
		c.add(id, "the index has no digest")
	}
	if x.Commit != r.Source.Commit || x.Content != r.Source.Content {
		c.add(id, fmt.Sprintf("index drift: the index was built from %s (content %.12s), the workspace is %s (content %.12s)",
			x.Commit, x.Content, r.Source.Commit, r.Source.Content))
	}
	switch run.Index {
	case "build":
		if x.Reused {
			c.add(id, "planned to build its index, the record reused one")
		}
		if x.BuildMS == nil {
			c.add(id, "an index build without its build time")
		}
	case "reuse":
		if !x.Reused {
			c.add(id, "planned to reuse the cell's index, the record built one")
		}
	}
	if run.Cache == "warm" {
		cell := run.Task + "." + run.Condition + "." + run.Backend
		if d, ok := digests[cell]; !ok {
			digests[cell] = x.Digest
		} else if d != x.Digest {
			c.add(id, fmt.Sprintf("index drift: digest %.12s, the warm cell's index is %.12s", x.Digest, d))
		}
	}
}

func (c *checker) attempts(r *Record, backend manifest.Backend) {
	id := r.RunID
	if len(r.Attempts) == 0 {
		c.add(id, "no attempts")
		return
	}
	if max := c.m.Execution.MaxAttempts; len(r.Attempts) > max {
		c.add(id, fmt.Sprintf("%d attempts, the policy allows %d", len(r.Attempts), max))
	}
	sem := SemanticsFor(backend.CLI)
	for i, a := range r.Attempts {
		if a.N != i+1 {
			c.add(id, fmt.Sprintf("attempt %d is numbered %d", i+1, a.N))
		}
		if a.WallMS < 0 || a.OutputBytes < 0 {
			c.add(id, fmt.Sprintf("attempt %d: negative time or size", i+1))
		}
		if a.Completed() && i < len(r.Attempts)-1 {
			c.add(id, fmt.Sprintf("attempt %d completed but was retried", i+1))
		}
		for _, p := range a.Usage.Problems(sem) {
			c.add(id, fmt.Sprintf("attempt %d usage: %s", i+1, p))
		}
		for _, p := range a.Cost.problems() {
			c.add(id, fmt.Sprintf("attempt %d cost: %s", i+1, p))
		}
	}
}

func (c *checker) checks(r *Record, task manifest.Task) {
	id := r.RunID
	if r.AnswerSHA256 != SHA256(r.Answer) {
		c.add(id, "the answer hash does not match the answer")
	}
	declared := map[string]manifest.Check{}
	for _, ch := range task.Checks {
		declared[ch.ID] = ch
	}
	got := map[string]bool{}
	for _, res := range r.Checks {
		ch, ok := declared[res.ID]
		if !ok {
			c.add(id, fmt.Sprintf("check %q not declared by task %q: inconsistent checks", res.ID, task.ID))
			continue
		}
		if got[res.ID] {
			c.add(id, fmt.Sprintf("check %q recorded twice", res.ID))
			continue
		}
		got[res.ID] = true
		if res.Type != ch.Type {
			c.add(id, fmt.Sprintf("check %q recorded as %s, declared as %s", res.ID, res.Type, ch.Type))
			continue
		}
		if res.OutputSHA256 != SHA256(res.Output) {
			c.add(id, fmt.Sprintf("check %q: the output hash does not match the output", res.ID))
		}
		if res.Content != r.After.Content {
			c.add(id, fmt.Sprintf("check %q ran on content %.12s, the run's tree is %.12s", res.ID, res.Content, r.After.Content))
		}
		if ch.Type == manifest.CheckFixTests && res.Overlay != task.Provenance.FixRevision {
			c.add(id, fmt.Sprintf("check %q: overlay %q, want the fix revision %s", res.ID, res.Overlay, task.Provenance.FixRevision))
		}
		if ch.Type == manifest.CheckAnswer && res.Output != r.Answer {
			c.add(id, fmt.Sprintf("check %q graded a text that is not the run's answer", res.ID))
		}
		if ch.Type == manifest.CheckReview && res.Passed && res.Reviewer == "" {
			c.add(id, fmt.Sprintf("check %q passed without a reviewer", res.ID))
		}
		if want := Passes(ch, res, r); ch.Type != manifest.CheckReview && res.Passed != want {
			c.add(id, fmt.Sprintf("check %q says passed=%v, its output says %v", res.ID, res.Passed, want))
		}
	}
	for _, ch := range task.Checks {
		if !got[ch.ID] {
			c.add(id, fmt.Sprintf("check %q missing: inconsistent checks", ch.ID))
		}
	}
}

// Passes recomputes an executable check from what it recorded, never from its passed flag: the
// exit code against the expected one, the answer against the expected strings, the tree against
// its commit. The runner sets a result's Passed with it, and Check holds the record to it.
func Passes(ch manifest.Check, res CheckResult, r *Record) bool {
	switch ch.Type {
	case manifest.CheckCommand, manifest.CheckFixTests:
		want := 0
		if ch.Expect.Exit != nil {
			want = *ch.Expect.Exit
		}
		return res.Exit != nil && *res.Exit == want
	case manifest.CheckAnswer:
		if res.Output != r.Answer {
			return false
		}
		for _, s := range ch.Expect.Contains {
			if !strings.Contains(r.Answer, s) {
				return false
			}
		}
		return true
	case manifest.CheckNoChanges:
		return r.After.Content == CleanContent
	}
	return false
}

// Success reports whether the run succeeded: it ran (was supported), its last attempt completed,
// every executable check passes when recomputed from its output, and it stayed isolated. Review
// checks are not gates and do not count.
func Success(m *manifest.Manifest, r *Record) bool {
	if r.Unsupported != "" || len(r.Attempts) == 0 || !r.Attempts[len(r.Attempts)-1].Completed() {
		return false
	}
	if len(r.Isolation.Violations) > 0 || !r.Isolation.Checked {
		return false
	}
	var task manifest.Task
	for _, t := range m.Tasks {
		if t.ID == r.Task {
			task = t
		}
	}
	gates := 0
	for _, ch := range task.Checks {
		if ch.Type == manifest.CheckReview {
			continue
		}
		i := slices.IndexFunc(r.Checks, func(res CheckResult) bool { return res.ID == ch.ID })
		if i < 0 || r.Checks[i].Type != ch.Type || r.Checks[i].OutputSHA256 != SHA256(r.Checks[i].Output) ||
			r.Checks[i].Content != r.After.Content || !Passes(ch, r.Checks[i], r) {
			return false
		}
		gates++
	}
	return gates > 0
}

// Tokens is a usage normalised across semantics: Fresh is input outside the cache, Total every
// token once.
type Tokens struct {
	Fresh, CacheCreation, CacheRead, Output, Total int64
}

// Problems lists what is wrong with u for a backend whose usage has the given semantics.
func (u Usage) Problems(semantics string) []string {
	var out []string
	counters := map[string]*int64{"input": u.Input, "cache_creation": u.CacheCreation, "cache_read": u.CacheRead,
		"output": u.Output, "total": u.Total, "last_turn_context": u.LastTurnContext}
	for _, name := range []string{"input", "cache_creation", "cache_read", "output", "total", "last_turn_context"} {
		if p := counters[name]; p != nil && *p < 0 {
			out = append(out, name+" is negative")
		}
	}
	switch u.Source {
	case Unavailable:
		if u.Input != nil || u.CacheCreation != nil || u.CacheRead != nil || u.Output != nil || u.Total != nil {
			out = append(out, "unavailable usage carries counters: mark it reported or estimated, or drop them")
		}
		return out
	case Estimated:
		if u.Estimator == "" {
			out = append(out, "an estimate names its estimator (bytes/4, tokenizer:<name>)")
		}
		if u.Total == nil {
			out = append(out, "an estimate has a total")
		}
		return out
	case Reported:
	default:
		return append(out, fmt.Sprintf("unknown source %q: reported, estimated or unavailable", u.Source))
	}
	if u.Semantics != semantics {
		return append(out, fmt.Sprintf("semantics %q, the backend reports %q", u.Semantics, semantics))
	}
	required := []string{"input", "cache_creation", "cache_read", "output"}
	if semantics == InputIncludesCache {
		required = []string{"input", "cache_read", "output"}
		if u.CacheCreation != nil {
			out = append(out, "input-includes-cache has no cache-creation counter")
		}
	}
	for _, name := range required {
		if counters[name] == nil {
			out = append(out, "incomplete counters: "+name+" missing (missing is not zero: mark the usage unavailable instead)")
		}
	}
	if len(out) > 0 {
		return out
	}
	if semantics == InputIncludesCache && *u.CacheRead > *u.Input {
		return append(out, fmt.Sprintf("cache reads %d exceed input %d, which already includes them", *u.CacheRead, *u.Input))
	}
	t, _ := u.tokens()
	if u.Total != nil && *u.Total != t.Total {
		if semantics == InputIncludesCache && *u.Total == t.Total+*u.CacheRead {
			out = append(out, fmt.Sprintf("total %d adds cached input twice: the input already includes the %d cached tokens", *u.Total, *u.CacheRead))
		} else {
			out = append(out, fmt.Sprintf("total %d, the counters give %d", *u.Total, t.Total))
		}
	}
	return out
}

// Tokens is u normalised, when u is reported and complete. Estimated and unavailable usage give
// none: an estimate is never a provider-reported token.
func (u Usage) Tokens() (Tokens, bool) {
	if u.Source != Reported || len(u.Problems(u.Semantics)) > 0 {
		return Tokens{}, false
	}
	return u.tokens()
}

func (u Usage) tokens() (Tokens, bool) {
	v := func(p *int64) int64 {
		if p == nil {
			return 0
		}
		return *p
	}
	switch u.Semantics {
	case InputExcludesCache:
		t := Tokens{Fresh: v(u.Input), CacheCreation: v(u.CacheCreation), CacheRead: v(u.CacheRead), Output: v(u.Output)}
		t.Total = t.Fresh + t.CacheCreation + t.CacheRead + t.Output
		return t, true
	case InputIncludesCache:
		t := Tokens{Fresh: v(u.Input) - v(u.CacheRead), CacheRead: v(u.CacheRead), Output: v(u.Output)}
		t.Total = v(u.Input) + t.Output
		return t, true
	}
	return Tokens{}, false
}

func (c Cost) problems() []string {
	switch c.Source {
	case Unavailable:
		if c.USD != nil {
			return []string{"unavailable cost carries an amount"}
		}
		return nil
	case Reported, Estimated:
	default:
		return []string{fmt.Sprintf("unknown source %q", c.Source)}
	}
	var out []string
	if !slices.Contains([]string{"spend", "api-equivalent", "subscription"}, c.Kind) {
		out = append(out, fmt.Sprintf("unknown kind %q: spend, api-equivalent or subscription", c.Kind))
	}
	if c.USD == nil && c.Kind != "subscription" {
		out = append(out, "no amount")
	}
	if c.Source == Estimated && (c.PriceDate == "" || c.Model == "" || c.ContextTier == "") {
		out = append(out, "an estimate names its price date, model and context tier")
	}
	return out
}
