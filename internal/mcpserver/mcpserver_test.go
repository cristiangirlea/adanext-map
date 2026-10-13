package mcpserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	scippb "github.com/scip-code/scip/bindings/go/scip"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

const (
	commitA = "0123456789abcdef0123456789abcdef01234567"
	commitB = "fedcba9876543210fedcba9876543210fedcba98"
)

// writeStore converts x and stores it as commit in repo, which makes it the latest.
func writeStore(t *testing.T, repo, commit string, x *scippb.Index) {
	t.Helper()
	idx, _, err := scip.Convert(x)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(repo, store.Header{Commit: commit, Indexer: idx.Tool}, idx); err != nil {
		t.Fatal(err)
	}
}

// callGraphRepo is a repository whose latest store is the call-graph fixture at commitA.
func callGraphRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.CallGraph())
	return repo
}

// connect starts a server for cfg over in-memory pipes and returns a client session to it.
func connect(t *testing.T, cfg Config) *mcp.ClientSession {
	t.Helper()
	if cfg.Version == "" {
		cfg.Version = "0.0.0-test"
	}
	ctx := context.Background()
	serverEnd, clientEnd := mcp.NewInMemoryTransports()
	ss, err := New(cfg).Connect(ctx, serverEnd, nil)
	if err != nil {
		t.Fatal(err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "0"}, nil).Connect(ctx, clientEnd, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		cs.Close()
		ss.Wait()
	})
	return cs
}

// answer is a tool call's result as a test reads it.
type answer struct {
	text       string
	isError    bool
	structured map[string]any
}

// call calls the tool and fails the test on a protocol error; a tool error is in the answer.
func call(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) answer {
	t.Helper()
	res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tool, Arguments: args})
	if err != nil {
		t.Fatalf("%s: protocol error: %v", tool, err)
	}
	var a answer
	a.isError = res.IsError
	for _, c := range res.Content {
		tc, ok := c.(*mcp.TextContent)
		if !ok {
			t.Fatalf("%s: content %T, want text", tool, c)
		}
		a.text += tc.Text
	}
	if res.StructuredContent != nil {
		data, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(data, &a.structured); err != nil {
			t.Fatalf("%s: structured content is not an object: %s", tool, data)
		}
	}
	return a
}

// ok calls the tool and fails the test unless it answers without a tool error.
func ok(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any) answer {
	t.Helper()
	a := call(t, cs, tool, args)
	if a.isError {
		t.Fatalf("%s %v: tool error: %s", tool, args, a.text)
	}
	return a
}

// wantToolError calls the tool and fails the test unless it gives a tool error holding every one of want.
func wantToolError(t *testing.T, cs *mcp.ClientSession, tool string, args map[string]any, want ...string) answer {
	t.Helper()
	a := call(t, cs, tool, args)
	if !a.isError {
		t.Fatalf("%s %v: want a tool error, got %q", tool, args, a.text)
	}
	for _, w := range want {
		if !strings.Contains(a.text, w) {
			t.Errorf("%s %v: the error should say %q, got %q", tool, args, w, a.text)
		}
	}
	return a
}

var toolNames = []string{"callers", "find_symbol", "impact", "map", "outline", "refs", "status"}

func TestServerIsNamedAdamapWithItsVersion(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Version: "1.2.3"})
	info := cs.InitializeResult().ServerInfo
	if info.Name != "adamap" || info.Version != "1.2.3" {
		t.Errorf("server info %+v, want adamap 1.2.3", info)
	}
}

func TestToolsAreListedWithDescriptionsAndSchemas(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	res, err := cs.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, tool := range res.Tools {
		names = append(names, tool.Name)
		if len(tool.Description) < 80 {
			t.Errorf("%s: description too short to guide a model: %q", tool.Name, tool.Description)
		}
		schema, isMap := tool.InputSchema.(map[string]any)
		if !isMap || schema["type"] != "object" {
			t.Errorf("%s: input schema %v, want an object schema", tool.Name, tool.InputSchema)
		}
		if tool.OutputSchema == nil {
			t.Errorf("%s: no output schema", tool.Name)
		}
		if tool.Annotations == nil || !tool.Annotations.ReadOnlyHint {
			t.Errorf("%s: should be marked read-only", tool.Name)
		}
		if props, _ := schema["properties"].(map[string]any); props != nil {
			for name, p := range props {
				if d, _ := p.(map[string]any)["description"].(string); d == "" {
					t.Errorf("%s: input %s has no description", tool.Name, name)
				}
			}
		}
	}
	slices.Sort(names)
	if !slices.Equal(names, toolNames) {
		t.Errorf("tools %v, want %v", names, toolNames)
	}
	for _, tool := range res.Tools {
		switch tool.Name {
		case "refs", "callers", "impact":
			if !strings.Contains(tool.Description, "SCIP symbol string") || !strings.Contains(tool.Description, "name") {
				t.Errorf("%s: description should say a symbol is a name or a SCIP symbol string: %q", tool.Name, tool.Description)
			}
		}
	}
}

func TestToolsAnswerFromTheStore(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"outline", map[string]any{"file": "calls/a.go"},
			"calls/a.go:2 Function A\ncalls/a.go:8 Function B\ncalls/a.go:14 Function D\n"},
		{"refs", map[string]any{"symbol": "C"},
			"calls/b.go:2 definition C\ncalls/b.go:11 reference C\ncalls/b.go:20 reference C\ncalls/a.go:10 reference C\n"},
		{"callers", map[string]any{"symbol": "C"},
			"calls/a.go:8 Function B refs=10\ncalls/b.go:8 Function Rec refs=11\ncalls/b.go:19 Method A refs=20\n"},
		{"impact", map[string]any{"target": "C"},
			"calls/a.go:8 Function B depth=1\ncalls/b.go:8 Function Rec depth=1\ncalls/b.go:19 Method A depth=1\n" +
				"calls/a.go:2 Function A depth=2\ncalls/b.go:17 Struct Thing depth=2\ncalls/a.go:14 Function D depth=3\n"},
		{"impact", map[string]any{"target": "C", "depth": 1},
			"calls/a.go:8 Function B depth=1\ncalls/b.go:8 Function Rec depth=1\ncalls/b.go:19 Method A depth=1\n"},
		{"refs", map[string]any{"symbol": sciptest.ThingA},
			"calls/b.go:19 definition A\n"},
	} {
		a := ok(t, cs, tc.tool, tc.args)
		if a.text != tc.want {
			t.Errorf("%s %v:\n got %q\nwant %q", tc.tool, tc.args, a.text, tc.want)
		}
		if a.structured["commit"] != commitA {
			t.Errorf("%s: structured content should name the commit, got %v", tc.tool, a.structured)
		}
	}
}

func TestFindSymbolListsMatchesAndSaysHowManyWereLeftOut(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	a := ok(t, cs, "find_symbol", map[string]any{"name": "a"})
	if !strings.HasPrefix(a.text, "calls/a.go:2 Function A\ncalls/b.go:19 Method A\n") {
		t.Errorf("find_symbol a: %q", a.text)
	}
	if a.structured["total"] != float64(2) || a.structured["query"] != "a" {
		t.Errorf("structured %v", a.structured)
	}
	a = ok(t, cs, "find_symbol", map[string]any{"name": "a", "limit": 1})
	if a.text != "calls/a.go:2 Function A\n(1 more match; raise limit to see it)\n" {
		t.Errorf("find_symbol a limit 1: %q", a.text)
	}
	a = ok(t, cs, "find_symbol", map[string]any{"name": "nothing-like-it"})
	if !strings.Contains(a.text, "no symbol matches") {
		t.Errorf("an empty answer should say so: %q", a.text)
	}
}

func TestImpactTakesAFile(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	a := ok(t, cs, "impact", map[string]any{"target": "calls/b.go"})
	if !strings.Contains(a.text, "calls/a.go:8 Function B depth=1") || a.structured["kind"] != "file" {
		t.Errorf("impact of a file: %q %v", a.text, a.structured)
	}
}

func TestPathsGoThroughThePathFunction(t *testing.T) {
	var seen []string
	cs := connect(t, Config{Repo: callGraphRepo(t), Path: func(arg string) (string, error) {
		seen = append(seen, arg)
		return strings.TrimPrefix(arg, "/abs/"), nil
	}})
	ok(t, cs, "outline", map[string]any{"file": "/abs/calls/a.go"})
	ok(t, cs, "impact", map[string]any{"target": "/abs/calls/b.go"})
	if !slices.Equal(seen, []string{"/abs/calls/a.go", "/abs/calls/b.go"}) {
		t.Errorf("Path saw %q", seen)
	}
}

func TestDefaultPathCleansAndSlashes(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	ok(t, cs, "outline", map[string]any{"file": "./calls/../calls/a.go"})
	if filepath.Separator == '\\' {
		ok(t, cs, "outline", map[string]any{"file": `calls\a.go`})
	}
}

func TestMapFitsTheTokensAndNamesTheCommit(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	a := ok(t, cs, "map", nil)
	if !strings.Contains(a.text, "calls/b.go\n") || !strings.Contains(a.text, "  func  ") {
		t.Errorf("map: %q", a.text)
	}
	if a.structured["commit"] != commitA || a.structured["tokens"] != float64(1024) {
		t.Errorf("map structured %v", a.structured)
	}
	small := ok(t, cs, "map", map[string]any{"tokens": 8})
	if len(small.text) > 8*4 || small.text == "" {
		t.Errorf("map with 8 tokens: %q", small.text)
	}
	a = ok(t, cs, "map", map[string]any{"for": "nothing-matches-this"})
	if !strings.Contains(a.text, "not personalised") {
		t.Errorf("a for that matches nothing should say so: %q", a.text)
	}
	a = ok(t, cs, "map", map[string]any{"tokens": 1})
	if !strings.Contains(a.text, "nothing fits") {
		t.Errorf("a budget too small should say so: %q", a.text)
	}
}

func TestBadArgumentsAreToolErrors(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	wantToolError(t, cs, "map", map[string]any{"tokens": -1}, "tokens")
	wantToolError(t, cs, "find_symbol", map[string]any{"name": "A", "limit": -1}, "limit")
	wantToolError(t, cs, "impact", map[string]any{"target": "C", "depth": -2}, "depth")
	wantToolError(t, cs, "refs", map[string]any{}, "symbol")
	wantToolError(t, cs, "find_symbol", map[string]any{"name": ""}, "name")
}

func TestUnknownFileAndSymbolAreToolErrors(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	wantToolError(t, cs, "outline", map[string]any{"file": "calls/nope.go"}, `"calls/nope.go"`, "0123456789ab")
	for _, tool := range []string{"refs", "callers"} {
		wantToolError(t, cs, tool, map[string]any{"symbol": "Nope"}, "Nope", "find_symbol")
	}
	wantToolError(t, cs, "impact", map[string]any{"target": "Nope"}, "Nope", "find_symbol")
}

func TestAmbiguousNameListsTheCandidates(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	for _, tool := range []string{"refs", "callers"} {
		wantToolError(t, cs, tool, map[string]any{"symbol": "A"}, "fits 2 symbols", sciptest.FnA, sciptest.ThingA)
	}
	wantToolError(t, cs, "impact", map[string]any{"target": "A"}, "fits 2 symbols", sciptest.FnA, sciptest.ThingA)
}

func TestMissingStoreIsAToolError(t *testing.T) {
	repo := t.TempDir()
	cs := connect(t, Config{Repo: repo})
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"map", nil},
		{"outline", map[string]any{"file": "a.go"}},
		{"find_symbol", map[string]any{"name": "A"}},
		{"refs", map[string]any{"symbol": "A"}},
		{"callers", map[string]any{"symbol": "A"}},
		{"impact", map[string]any{"target": "A"}},
	} {
		wantToolError(t, cs, tc.tool, tc.args, "adamap index")
	}
	a := ok(t, cs, "status", nil)
	if !strings.Contains(a.text, "no store") || !strings.Contains(a.text, "adamap index") {
		t.Errorf("status without a store: %q", a.text)
	}
	if a.structured["store"] != false || a.structured["stale"] != true {
		t.Errorf("status structured %v", a.structured)
	}

	// The store appears later: the next call finds it without a restart.
	writeStore(t, repo, commitA, sciptest.CallGraph())
	ok(t, cs, "outline", map[string]any{"file": "calls/a.go"})
}

func TestPinnedCommitThatHasNoStoreIsAToolError(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Commit: commitB})
	wantToolError(t, cs, "outline", map[string]any{"file": "calls/a.go"}, "adamap index")
}

// touchLatest moves .adamap/latest's mtime on, so a change is seen even on a file system whose
// clock is coarser than the test.
func touchLatest(t *testing.T, repo string, by time.Duration) {
	t.Helper()
	p := filepath.Join(repo, store.DirName, "latest")
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(p, fi.ModTime().Add(by), fi.ModTime().Add(by)); err != nil {
		t.Fatal(err)
	}
}

func TestANewLatestStoreIsUsedOnTheNextCall(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	cs := connect(t, Config{Repo: repo})
	ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})
	wantToolError(t, cs, "outline", map[string]any{"file": "calls/a.go"})

	writeStore(t, repo, commitB, sciptest.CallGraph())
	touchLatest(t, repo, time.Minute)
	a := ok(t, cs, "outline", map[string]any{"file": "calls/a.go"})
	if a.structured["commit"] != commitB {
		t.Errorf("after the new store, the answer should come from %s: %v", commitB, a.structured)
	}
	if s := ok(t, cs, "status", nil); s.structured["commit"] != commitB {
		t.Errorf("status after the new store: %v", s.structured)
	}

	// The same commit indexed again, with other content, is reloaded too.
	writeStore(t, repo, commitB, sciptest.Index())
	touchLatest(t, repo, 2*time.Minute)
	ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})
}

// pointLatest makes .adamap/latest name commit, through a temporary file and a rename as
// store.Write does, with an mtime that differs from every earlier call's.
func pointLatest(t *testing.T, repo, commit string, mod time.Time) {
	t.Helper()
	dir := filepath.Join(repo, store.DirName)
	tmp := filepath.Join(dir, ".latest.test.tmp")
	if err := os.WriteFile(tmp, []byte(commit+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(tmp, mod, mod); err != nil {
		t.Fatal(err)
	}
	// Windows refuses a rename over a file a reader has open at that instant: try again.
	var err error
	for try := 0; try < 200; try++ {
		if err = os.Rename(tmp, filepath.Join(dir, "latest")); err == nil {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal(err)
}

func TestConcurrentCallsShareTheStoreWhileLatestMoves(t *testing.T) {
	repo := callGraphRepo(t)
	writeStore(t, repo, commitB, sciptest.CallGraph())
	cs := connect(t, Config{Repo: repo})

	const workers, rounds = 8, 50
	problems := make(chan string, workers*rounds*2)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < rounds; i++ {
				for _, tc := range []struct {
					tool string
					args map[string]any
				}{
					{"outline", map[string]any{"file": "calls/a.go"}},
					{"map", nil},
				} {
					res, err := cs.CallTool(context.Background(), &mcp.CallToolParams{Name: tc.tool, Arguments: tc.args})
					if err != nil {
						problems <- tc.tool + ": protocol error: " + err.Error()
						continue
					}
					if res.IsError {
						msg := ""
						for _, c := range res.Content {
							if text, ok := c.(*mcp.TextContent); ok {
								msg += text.Text
							}
						}
						// Windows fails an open of a file at the instant it is renamed over; POSIX never does.
						if runtime.GOOS == "windows" && strings.Contains(msg, "used by another process") {
							continue
						}
						problems <- tc.tool + ": tool error: " + msg
						continue
					}
					sc, _ := res.StructuredContent.(map[string]any)
					if commit, _ := sc["commit"].(string); commit != commitA && commit != commitB {
						problems <- fmt.Sprintf("%s: answer names commit %q, want %s or %s", tc.tool, commit, commitA, commitB)
					}
				}
			}
		}()
	}

	// Meanwhile latest moves between the two stores, each time with a new mtime so the server reloads.
	base := time.Now()
	for i := 0; i < 2*rounds; i++ {
		commit := commitB
		if i%2 == 1 {
			commit = commitA
		}
		pointLatest(t, repo, commit, base.Add(time.Duration(i+1)*time.Minute))
	}
	wg.Wait()
	close(problems)
	for p := range problems {
		t.Error(p)
	}
}

func TestPinnedCommitIgnoresLatest(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	cs := connect(t, Config{Repo: repo, Commit: commitA})
	writeStore(t, repo, commitB, sciptest.CallGraph())
	touchLatest(t, repo, time.Minute)
	a := ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})
	if a.structured["commit"] != commitA {
		t.Errorf("a pinned server should stay on %s: %v", commitA, a.structured)
	}
	if s := ok(t, cs, "status", nil); s.structured["pinned"] != true {
		t.Errorf("status should say the commit is pinned: %v", s.structured)
	}
}

func TestStatusSaysWhetherTheStoreIsCurrent(t *testing.T) {
	repo := callGraphRepo(t)
	for _, tc := range []struct {
		name        string
		head        string
		dirty       bool
		stale       bool
		wantInText  string
		wantRefresh bool
	}{
		{"current", commitA, false, false, "current", false},
		{"behind HEAD", commitB, false, true, "not HEAD", true},
		{"dirty tree", commitA, true, true, "uncommitted changes", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := connect(t, Config{Repo: repo, Head: func() (string, bool, error) { return tc.head, tc.dirty, nil }})
			a := ok(t, cs, "status", nil)
			s := a.structured
			if s["commit"] != commitA || s["head"] != tc.head || s["at_head"] != (tc.head == commitA) ||
				s["dirty"] != tc.dirty || s["stale"] != tc.stale || s["store"] != true {
				t.Errorf("status %v", s)
			}
			counts, _ := s["counts"].(map[string]any)
			if counts["documents"] != float64(3) || counts["occurrences"] == float64(0) {
				t.Errorf("status counts %v", counts)
			}
			if !strings.Contains(a.text, tc.wantInText) || !strings.Contains(a.text, "0123456789ab") {
				t.Errorf("status text %q, want %q in it", a.text, tc.wantInText)
			}
			refresh, _ := s["refresh"].(string)
			if tc.wantRefresh != strings.Contains(refresh, "adamap index --repo") ||
				tc.wantRefresh != strings.Contains(a.text, "adamap index --repo") {
				t.Errorf("refresh %q in %q, want it: %v", refresh, a.text, tc.wantRefresh)
			}
		})
	}
}

func TestStatusWithoutGit(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Head: func() (string, bool, error) {
		return "", false, os.ErrNotExist
	}})
	a := ok(t, cs, "status", nil)
	if a.structured["git_error"] == nil || a.structured["store"] != true {
		t.Errorf("status without git: %v", a.structured)
	}
}

// hostileRepo holds a store whose names and kind carry a raw ESC, BEL and a bidi override and whose
// path a zero-width space (a control character in a path would not load).
func hostileRepo(t *testing.T) string {
	t.Helper()
	repo := t.TempDir()
	def := model.Range{StartLine: 3, StartCol: 5, EndLine: 3, EndCol: 9}
	idx := &model.Index{
		Documents: []model.Document{
			{Path: "d\u202e/f.go", Occurrences: []model.Occurrence{
				{Range: def, Symbol: "s m p v esc().", Roles: model.RoleDefinition},
				{Range: model.Range{StartLine: 8, EndLine: 8, EndCol: 4}, Symbol: "s m p v esc().", Roles: model.RoleReference},
				{Range: model.Range{StartLine: 9, EndLine: 9, EndCol: 4}, Symbol: "s m p v bidi#", Roles: model.RoleDefinition},
				{Range: model.Range{StartLine: 10, EndLine: 10, EndCol: 4}, Symbol: "s m p v bidi2#", Roles: model.RoleDefinition},
			}},
			{Path: "g.go", Occurrences: []model.Occurrence{
				{Range: model.Range{StartLine: 1, EndLine: 1, EndCol: 4}, Symbol: "s m p v esc().", Roles: model.RoleReference},
			}},
		},
		Symbols: []model.Symbol{
			{ID: "s m p v bidi#", DisplayName: "dup\u202e", Kind: "Struct"},
			{ID: "s m p v bidi2#", DisplayName: "dup\u202e", Kind: "Struct"},
			{ID: "s m p v esc().", DisplayName: "ev\x1b[2Jil", Kind: "Fun\x1bction", Signature: "func ev\x1b[2Jil()",
				Doc: "doc\x1b[31m", Definition: &model.Location{Path: "d\u202e/f.go", Range: def}},
		},
	}
	if _, err := store.Write(repo, store.Header{Commit: commitA, Indexer: model.Tool{Name: "x\x1by"}}, idx); err != nil {
		t.Fatal(err)
	}
	return repo
}

func TestHostileStoreStringsAreEscapedInTextContent(t *testing.T) {
	cs := connect(t, Config{Repo: hostileRepo(t)})
	for _, tc := range []struct {
		tool string
		args map[string]any
	}{
		{"map", nil},
		{"outline", map[string]any{"file": "d\u202e/f.go"}},
		{"find_symbol", map[string]any{"name": "ev"}},
		{"find_symbol", map[string]any{"name": "dup"}},
		{"refs", map[string]any{"symbol": "s m p v esc()."}},
		{"callers", map[string]any{"symbol": "s m p v esc()."}},
		{"impact", map[string]any{"target": "s m p v esc()."}},
		{"refs", map[string]any{"symbol": "dup\u202e"}},
		{"outline", map[string]any{"file": "nope\x1b.go"}},
		{"status", nil},
	} {
		a := call(t, cs, tc.tool, tc.args)
		if strings.ContainsAny(a.text, "\x1b\x07\u202e") {
			t.Errorf("%s %q: text content holds a raw control or format character: %q", tc.tool, tc.args, a.text)
		}
		if a.text == "" {
			t.Errorf("%s %q: no text content", tc.tool, tc.args)
		}
	}
	a := ok(t, cs, "outline", map[string]any{"file": "d\u202e/f.go"})
	if !strings.Contains(a.text, `"d\u202e/f.go"`) {
		t.Errorf("the hostile path should be quoted the CLI's way: %q", a.text)
	}
}

func TestErrorTextIsEscapedToo(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Path: func(arg string) (string, error) {
		return "", errors.New("cannot use " + arg + "\nsecond line")
	}})
	a := wantToolError(t, cs, "outline", map[string]any{"file": "x\x1b[2Jy"}, `"cannot use x\x1b[2Jy"`, "\nsecond line")
	if strings.Contains(a.text, "\x1b") {
		t.Errorf("a raw ESC reached the error text: %q", a.text)
	}
}

func TestTheStoreIsLoadedWhenTheServerStarts(t *testing.T) {
	repo := callGraphRepo(t)
	cs := connect(t, Config{Repo: repo})
	// With the store gone from disk, only the copy loaded at the start can answer.
	if err := os.RemoveAll(filepath.Join(repo, store.DirName)); err != nil {
		t.Fatal(err)
	}
	ok(t, cs, "outline", map[string]any{"file": "calls/a.go"})
}

func TestStatusOfAStoreIndexedFromTheDirtyTreeItDescribes(t *testing.T) {
	repo := t.TempDir()
	idx, _, err := scip.Convert(sciptest.CallGraph())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Write(repo, store.Header{Commit: commitA, Dirty: true}, idx); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name      string
		treeDirty bool
		stale     bool
		want      string
	}{
		// Indexing again cannot clear a dirty tree, so the store is not called stale; the text
		// says what adamap cannot tell.
		{"tree still dirty", true, false, "both have uncommitted changes"},
		{"tree clean now", false, true, "stale: the store was indexed with uncommitted changes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cs := connect(t, Config{Repo: repo, Head: func() (string, bool, error) { return commitA, tc.treeDirty, nil }})
			a := ok(t, cs, "status", nil)
			if a.structured["stale"] != tc.stale || a.structured["store_dirty"] != true {
				t.Errorf("status %v", a.structured)
			}
			if !strings.Contains(a.text, tc.want) || !strings.Contains(a.text, "adamap index --repo") {
				t.Errorf("status text %q, want %q and the refresh command", a.text, tc.want)
			}
			if refresh, _ := a.structured["refresh"].(string); !strings.Contains(refresh, "adamap index --repo") {
				t.Errorf("structured refresh %q, want the command", refresh)
			}
		})
	}
}

func TestStatusOfAPinnedStaleStoreSaysToRestart(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Commit: commitA,
		Head: func() (string, bool, error) { return commitB, false, nil }})
	if a := ok(t, cs, "status", nil); !strings.Contains(a.text, "restart it without --commit") {
		t.Errorf("status text %q", a.text)
	}
}

func TestStatusWithoutGitCannotTell(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t), Head: func() (string, bool, error) {
		return "", false, errors.New("not a git repository")
	}})
	a := ok(t, cs, "status", nil)
	if !strings.Contains(a.text, "cannot tell") || !strings.Contains(a.text, "not a git repository") {
		t.Errorf("status text %q", a.text)
	}
}

func TestANewStoreThatFailsToLoadLeavesThePreviousOneServing(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	cs := connect(t, Config{Repo: repo, Head: func() (string, bool, error) { return commitB, false, nil }})
	ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})

	// B is complete, then its store.json.gz is damaged, and latest names it.
	writeStore(t, repo, commitB, sciptest.CallGraph())
	file := filepath.Join(store.Dir(repo, commitB), "store.json.gz")
	good, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("not gzip"), 0o644); err != nil {
		t.Fatal(err)
	}
	touchLatest(t, repo, time.Minute)

	for tool, args := range map[string]map[string]any{"outline": {"file": "pkg/a.go"}, "map": nil, "status": nil} {
		if a := ok(t, cs, tool, args); a.structured["commit"] != commitA {
			t.Errorf("%s: the previous store should keep serving while the new one fails: %v", tool, a.structured)
		}
	}
	s := ok(t, cs, "status", nil)
	failure, _ := s.structured["newer_store_error"].(string)
	if !strings.Contains(failure, "store.json.gz") || !strings.Contains(s.text, "failed to load") ||
		!strings.Contains(s.text, "store.json.gz") || s.structured["store"] != true || s.structured["stale"] != true {
		t.Errorf("status should say the newer store failed to load, and why: %q %v", s.text, s.structured)
	}
	if !strings.Contains(s.text, "refresh: adamap index --repo") {
		t.Errorf("status should offer the refresh: %q", s.text)
	}

	// The failure is remembered: B mended without latest changing is not tried again.
	if err := os.WriteFile(file, good, 0o644); err != nil {
		t.Fatal(err)
	}
	if a := ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"}); a.structured["commit"] != commitA {
		t.Errorf("a failed stamp should not be retried: %v", a.structured)
	}
	// Once latest changes again, the load is tried again, and succeeds.
	touchLatest(t, repo, 2*time.Minute)
	a := ok(t, cs, "outline", map[string]any{"file": "calls/a.go"})
	if a.structured["commit"] != commitB {
		t.Errorf("after latest changed, the mended store should serve: %v", a.structured)
	}
	if s := ok(t, cs, "status", nil); s.structured["newer_store_error"] != nil || strings.Contains(s.text, "failed to load") {
		t.Errorf("status should drop the failure once the store loads: %q %v", s.text, s.structured)
	}
}

func TestAnUnreadableLatestIsTriedAgainEvenAtTheSameStamp(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	writeStore(t, repo, commitB, sciptest.CallGraph())
	pointLatest(t, repo, commitA, time.Now().Add(-time.Hour))
	cs := connect(t, Config{Repo: repo})
	ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})

	// Latest holds something that is not a commit (as long as one, so its size is unchanged).
	at := time.Now().Add(time.Minute)
	pointLatest(t, repo, strings.Repeat("x", len(commitA)), at)
	a := ok(t, cs, "status", nil)
	if a.structured["commit"] != commitA || a.structured["newer_store_error"] == nil {
		t.Errorf("an unreadable latest should leave the previous store serving and be reported: %v", a.structured)
	}

	// A good latest with the very same stamp: unlike a store that failed to load, this is not remembered.
	pointLatest(t, repo, commitB, at)
	if a := ok(t, cs, "outline", map[string]any{"file": "calls/a.go"}); a.structured["commit"] != commitB {
		t.Errorf("latest was mended at the same stamp and should be read again: %v", a.structured)
	}
}

func TestReindexingTheSameCommitIsNoticedWhenLatestLooksUnchanged(t *testing.T) {
	repo := t.TempDir()
	writeStore(t, repo, commitA, sciptest.Index())
	latest := filepath.Join(repo, store.DirName, "latest")
	fi, err := os.Stat(latest)
	if err != nil {
		t.Fatal(err)
	}
	cs := connect(t, Config{Repo: repo})
	ok(t, cs, "outline", map[string]any{"file": "pkg/a.go"})

	// The same commit indexed again: latest is rewritten with the same size, and on a file system
	// with a coarse clock with the same modification time. Only the header's time tells.
	writeStore(t, repo, commitA, sciptest.CallGraph())
	if err := os.Chtimes(latest, fi.ModTime(), fi.ModTime()); err != nil {
		t.Fatal(err)
	}
	header := filepath.Join(store.Dir(repo, commitA), "header.json")
	if err := os.Chtimes(header, fi.ModTime().Add(time.Minute), fi.ModTime().Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	ok(t, cs, "outline", map[string]any{"file": "calls/a.go"})
}

// hardPath is a path that holds what a shell would act on: a space, $, a backtick, ", ;, ', and
// non-ASCII text. (Windows cannot name a file with a " in it, but the quoting must hold anyway.)
const hardPath = "/work/my proj $HOME `id` \"q\" a;b 'c' ünï"

// runShell runs script in the shell named, which the test skips without, and returns what it printed.
func runShell(t *testing.T, name string, args ...string) string {
	t.Helper()
	exe, err := exec.LookPath(name)
	if err != nil {
		t.Skipf("no %s here: %v", name, err)
	}
	cmd := exec.Command(exe, args...)
	cmd.Env = append(os.Environ(), "MSYS_NO_PATHCONV=1", "MSYS2_ARG_CONV_EXCL=*")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("%s %q: %v\n%s", name, args, err, out)
	}
	return string(out)
}

// shellReads is what the shell on this system reads back from the argument shellArg made of p.
func shellReads(t *testing.T, p string) string {
	t.Helper()
	if runtime.GOOS != "windows" {
		return runShell(t, "sh", "-c", "printf %s "+shellArg(p))
	}
	// PowerShell: pass it to a function as a command argument, as the refresh command passes it to
	// adamap, and print its UTF-16 code units, which survive any console code page.
	script := "function f { $args[0] }; $s = f " + shellArg(p) + "; ($s.ToCharArray() | ForEach-Object { [int]$_ }) -join ','"
	units := utf16.Encode([]rune(script))
	raw := make([]byte, 0, 2*len(units))
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	out := runShell(t, "powershell", "-NoProfile", "-NonInteractive", "-EncodedCommand", base64.StdEncoding.EncodeToString(raw))
	var b strings.Builder
	if strings.TrimSpace(out) == "" {
		return "" // no code units: an empty argument
	}
	for _, f := range strings.Split(strings.TrimSpace(out), ",") {
		n, err := strconv.Atoi(f)
		if err != nil {
			t.Fatalf("PowerShell printed %q: %v", out, err)
		}
		b.WriteRune(rune(n))
	}
	return b.String()
}

func TestTheRefreshCommandQuotesThePathForTheShell(t *testing.T) {
	for _, p := range []string{hardPath, "/work/plain", "/work/it's", `/work/back\slash`, "/work/tab\there", ""} {
		if got := shellReads(t, p); got != p {
			t.Errorf("shellArg(%q) is read back by the shell as %q", p, got)
		}
	}
}

func TestPOSIXAndPowerShellQuotingHaveTheirExactForms(t *testing.T) {
	for _, tc := range []struct{ in, posix, powershell string }{
		{"/work/plain", "/work/plain", "/work/plain"},
		{`C:\work\plain`, `'C:\work\plain'`, `C:\work\plain`},
		{"", "''", "''"},
		{"/work/it's", `'/work/it'\''s'`, "'/work/it''s'"},
		{"a b", "'a b'", "'a b'"},
		{"$HOME `id`", "'$HOME `id`'", "'$HOME `id`'"},
		{`a"b;c`, `'a"b;c'`, `'a"b;c'`},
		{"a\u2019b", "'a\u2019b'", "'a\u2019\u2019b'"}, // PowerShell reads a typographic quote as a quote
		{hardPath, `'/work/my proj $HOME ` + "`id`" + ` "q" a;b '\''c'\'' ünï'`, `'/work/my proj $HOME ` + "`id`" + ` "q" a;b ''c'' ünï'`},
	} {
		if got := posixArg(tc.in); got != tc.posix {
			t.Errorf("posixArg(%q) = %s, want %s", tc.in, got, tc.posix)
		}
		if got := powershellArg(tc.in); got != tc.powershell {
			t.Errorf("powershellArg(%q) = %s, want %s", tc.in, got, tc.powershell)
		}
	}
}

func TestPOSIXQuotingIsReadBackByASh(t *testing.T) {
	for _, p := range []string{hardPath, "/work/plain", "/work/it's", `/work/back\slash`, "/work/new\nline", ""} {
		if got := runShell(t, "sh", "-c", "printf %s "+posixArg(p)); got != p {
			t.Errorf("posixArg(%q) is read back by sh as %q", p, got)
		}
	}
}

func TestStatusQuotesARepositoryPathThatHoldsWhatAShellActsOn(t *testing.T) {
	// No " in the name: Windows cannot have one in a file name.
	repo := filepath.Join(t.TempDir(), "my proj $HOME `id` a;b 'c' ünï")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	cs := connect(t, Config{Repo: repo})
	a := ok(t, cs, "status", nil)
	abs, err := filepath.Abs(repo)
	if err != nil {
		t.Fatal(err)
	}
	want := "adamap index --repo " + shellArg(abs)
	if a.structured["refresh"] != want || !strings.Contains(a.text, "refresh: "+want+"\n") {
		t.Errorf("refresh %v in %q, want %q", a.structured["refresh"], a.text, want)
	}
	if got := shellReads(t, abs); got != abs {
		t.Errorf("the shell reads the path back as %q, want %q", got, abs)
	}
}

func TestImpactOfAnAbsolutePathOutsideTheRepositoryIsAToolError(t *testing.T) {
	repo := callGraphRepo(t)
	outside := filepath.Join(t.TempDir(), "elsewhere.go") // another directory, so outside repo
	// The path function of `adamap mcp` refuses a path outside the repository.
	cs := connect(t, Config{Repo: repo, Path: func(arg string) (string, error) {
		if filepath.IsAbs(arg) {
			return "", fmt.Errorf("%s is outside the repository %s", arg, repo)
		}
		return arg, nil
	}})
	wantToolError(t, cs, "impact", map[string]any{"target": outside}, "elsewhere.go")
	// With the default path function the path is no document and no symbol either.
	cs = connect(t, Config{Repo: repo})
	wantToolError(t, cs, "impact", map[string]any{"target": outside}, "elsewhere.go")
	wantToolError(t, cs, "outline", map[string]any{"file": outside}, "elsewhere.go")
}

func TestAWronglyTypedArgumentIsAToolError(t *testing.T) {
	cs := connect(t, Config{Repo: callGraphRepo(t)})
	for _, tc := range []struct {
		tool string
		args map[string]any
		want string
	}{
		{"map", map[string]any{"tokens": "many"}, "tokens"},
		{"map", map[string]any{"tokens": 1.5}, "tokens"},
		{"map", map[string]any{"for": 7}, "for"},
		{"find_symbol", map[string]any{"name": 5}, "name"},
		{"find_symbol", map[string]any{"name": "A", "limit": []string{"3"}}, "limit"},
		{"outline", map[string]any{"file": true}, "file"},
		{"refs", map[string]any{"symbol": map[string]any{"a": 1}}, "symbol"},
		{"impact", map[string]any{"target": "C", "depth": "deep"}, "depth"},
	} {
		wantToolError(t, cs, tc.tool, tc.args, tc.want)
	}
}

func TestStatusWithNoStoreAndGitFailingStillAnswers(t *testing.T) {
	cs := connect(t, Config{Repo: t.TempDir(), Head: func() (string, bool, error) {
		return "", false, errors.New("git: not a git repository")
	}})
	a := ok(t, cs, "status", nil)
	for _, want := range []string{"no store", "adamap index", "HEAD: unknown", "not a git repository", "refresh: adamap index --repo"} {
		if !strings.Contains(a.text, want) {
			t.Errorf("status text should hold %q: %q", want, a.text)
		}
	}
	s := a.structured
	if s["store"] != false || s["stale"] != true || s["git_error"] != "git: not a git repository" ||
		s["store_error"] == nil || s["head"] != nil || s["at_head"] != false {
		t.Errorf("status structured %v", s)
	}
	if refresh, _ := s["refresh"].(string); !strings.Contains(refresh, "adamap index --repo") {
		t.Errorf("the refresh command should be offered: %v", s)
	}
}
