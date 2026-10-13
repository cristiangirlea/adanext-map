// Package mcpserver serves adamap's map and queries as MCP tools: map, outline, find_symbol, refs,
// callers, impact and status. Each answer is the compact text the CLI prints, as text content, and
// the CLI's JSON, as structured content. The store is loaded once and loaded again when
// .adamap/latest changes (stores.go). Text built from the store goes through the CLI's escaping,
// since the store may come from a hostile repository and a host may echo the text to a terminal.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/query"
	"github.com/cristiangirlea/adanext-map/internal/rank"
)

// Config says which store the server answers from, and how it reaches what lies outside the store.
type Config struct {
	// Repo is the repository root, whose .adamap directory holds the stores.
	Repo string
	// Commit pins the server to that commit's store; empty follows .adamap/latest.
	Commit string
	// Version is adamap's version, reported to the client.
	Version string
	// Path turns a file argument as the model typed it into the index's form: slash-separated and
	// relative to the repository root. Nil cleans it and turns its separators into slashes.
	Path func(arg string) (string, error)
	// Head is the repository's HEAD commit and whether tracked files have uncommitted changes, for
	// the status tool. Nil means git is not asked.
	Head func() (commit string, dirty bool, err error)
}

// defaultTokens is the map's budget when the call gives none, as `adamap map`'s.
const defaultTokens = 1024

// defaultFindLimit is how many matches find_symbol lists when the call gives no limit, as `adamap
// find`'s.
const defaultFindLimit = 25

const instructions = `adamap answers code-navigation questions about this repository from a compiler-grade index
(SCIP), built per commit. Start a task with map to see the files and symbols that matter. Use
find_symbol, refs, callers and impact instead of grep (they are exact, and skip comments and strings),
and outline instead of reading a whole file to see what it defines. Answers describe the indexed
commit: status says whether that is still the repository's HEAD.`

// server is the state the tools share.
type server struct {
	cfg    Config
	repo   string // cfg.Repo made absolute, for messages
	stores *stores
}

// New makes the server and loads the store. When there is no store yet, or it does not load, each
// call tries again and, until one succeeds, gets a tool error saying why (no store says to run
// `adamap index`).
func New(cfg Config) *mcp.Server {
	if cfg.Path == nil {
		cfg.Path = func(arg string) (string, error) { return path.Clean(filepath.ToSlash(arg)), nil }
	}
	repo, err := filepath.Abs(cfg.Repo)
	if err != nil {
		repo = cfg.Repo
	}
	s := &server{cfg: cfg, repo: repo, stores: &stores{repo: cfg.Repo, commit: cfg.Commit}}
	s.stores.get() // the error, if any, is each call's to report
	srv := mcp.NewServer(
		&mcp.Implementation{Name: "adamap", Version: cfg.Version},
		&mcp.ServerOptions{Instructions: instructions, Capabilities: &mcp.ServerCapabilities{}},
	)
	readOnly := func(name, title, description string) *mcp.Tool {
		closed := false
		return &mcp.Tool{Name: name, Description: description, Annotations: &mcp.ToolAnnotations{
			Title: title, ReadOnlyHint: true, IdempotentHint: true, OpenWorldHint: &closed,
		}}
	}
	mcp.AddTool(srv, readOnly("map", "Repository map", mapDescription), s.mapTool)
	mcp.AddTool(srv, readOnly("outline", "Outline of a file", outlineDescription), s.outline)
	mcp.AddTool(srv, readOnly("find_symbol", "Find a symbol by name", findDescription), s.find)
	mcp.AddTool(srv, readOnly("refs", "References to a symbol", refsDescription), s.refs)
	mcp.AddTool(srv, readOnly("callers", "Callers of a symbol", callersDescription), s.callers)
	mcp.AddTool(srv, readOnly("impact", "What depends on a symbol or file", impactDescription), s.impact)
	mcp.AddTool(srv, readOnly("status", "Index status", statusDescription), s.status)
	return srv
}

const symbolArg = "`symbol` is a display name that fits exactly one symbol (such as `Load`), or a " +
	"SCIP symbol string as find_symbol prints it; a name that fits several symbols is refused with " +
	"the candidates and their symbol strings listed."

const (
	mapDescription = "A ranked map of the repository: the files that matter most (PageRank over which files " +
		"reference which), each with its most important symbols and their signatures, cut to a token " +
		"budget. Call it first on an unfamiliar repository or at the start of a task, instead of listing " +
		"directories and opening files to find your way; pass `for` with words of the task to rank the " +
		"files about it higher. Returns a file path per line, then an indented line per symbol: its kind " +
		"and signature."
	outlineDescription = "What a file defines: its global symbols (functions, types, methods, fields) by line, one per " +
		"line as `path:line kind name`. Use it instead of reading a whole file to learn what is in it or " +
		"where a definition starts. `file` is a path relative to the repository root."
	findDescription = "Find symbols by name: names equal to it first, then equal ignoring case, then starting with it, " +
		"then containing it. Returns `path:line kind name` per line, best first, at most `limit` (default " +
		"25), and says how many more there are. Use it instead of grep to locate a definition, and to get " +
		"the exact name or SCIP symbol string that refs, callers and impact take."
	refsDescription = "Every occurrence of a symbol, from the compiler's index: definitions first, then each use, " +
		"grouped by file, one per line as `path:line kind name`, where kind is definition, import, write, " +
		"read or reference. Use it instead of grep, which also matches comments, strings and other " +
		"symbols of the same name. " + symbolArg
	callersDescription = "Who calls or uses a symbol: the functions and other definitions whose body references it, one " +
		"per line as `path:line kind name refs=<lines of the references>`. Use it to see how something is " +
		"used before changing it, instead of grepping for its name. " + symbolArg
	impactDescription = "What depends on a symbol or a file through callers, transitively: direct callers at depth 1, " +
		"their callers at depth 2, down to `depth` (default 3). One line per symbol, nearest first, as " +
		"`path:line kind name depth=n`. Use it before a change to see what it can break and which code " +
		"and tests to look at. `target` is a file path in the index, a display name that fits exactly one " +
		"symbol, or a SCIP symbol string; an ambiguous name is refused with the candidates listed."
	statusDescription = "Which commit adamap's index describes, whether that is the repository's HEAD, whether the " +
		"working tree has uncommitted changes, the index's size, and, when the index is stale, the command " +
		"that refreshes it. Call it when an answer looks out of date or a file or symbol you expect is " +
		"missing."
)

// The tools' inputs. A field without omitempty is required.
type (
	mapIn struct {
		Tokens int    `json:"tokens,omitempty" jsonschema:"the most tokens the map may take, estimated as bytes / 4 (default 1024)"`
		For    string `json:"for,omitempty" jsonschema:"words of the task, separated by spaces or commas: files whose path or symbols contain one rank higher"`
	}
	outlineIn struct {
		File string `json:"file" jsonschema:"the file's path, relative to the repository root"`
	}
	findIn struct {
		Name  string `json:"name" jsonschema:"the name or part of a name to look for"`
		Limit int    `json:"limit,omitempty" jsonschema:"list at most this many matches (default 25)"`
	}
	symbolIn struct {
		Symbol string `json:"symbol" jsonschema:"a display name that fits exactly one symbol, or a SCIP symbol string"`
	}
	impactIn struct {
		Target string `json:"target" jsonschema:"a file path in the index, a display name that fits exactly one symbol, or a SCIP symbol string"`
		Depth  int    `json:"depth,omitempty" jsonschema:"follow callers at most this many calls away (default 3)"`
	}
	statusIn struct{}
)

// Meta heads every structured answer, as it heads the CLI's JSON: the commit of the store that
// answered, and whether that store was indexed with uncommitted changes.
type Meta struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty,omitempty"`
}

func meta(snap *snapshot) Meta { return Meta{snap.header.Commit, snap.header.Dirty} }

// The structured answers: the CLI's JSON for the same question (docs/queries.md, docs/map.md).
type (
	outlineOut struct {
		Meta
		Path    string         `json:"path"`
		Symbols []query.Symbol `json:"symbols"`
	}
	findOut struct {
		Meta
		query.FindResult
	}
	refsOut struct {
		Meta
		query.RefsResult
	}
	callersOut struct {
		Meta
		query.CallersResult
	}
	impactOut struct {
		Meta
		query.ImpactResult
	}
	mapOut struct {
		Commit   string    `json:"commit"`
		Tokens   int       `json:"tokens"`
		Estimate int       `json:"estimate"`
		For      []string  `json:"for,omitempty"`
		Matched  int       `json:"matched,omitempty"`
		Files    []mapFile `json:"files"`
	}
	mapFile struct {
		Path    string      `json:"path"`
		Rank    float64     `json:"rank"`
		Symbols []mapSymbol `json:"symbols"`
	}
	mapSymbol struct {
		ID     string  `json:"id"`
		Kind   string  `json:"kind"`
		Name   string  `json:"name"`
		Detail string  `json:"detail"`
		Refs   int     `json:"refs"`
		Score  float64 `json:"score"`
	}
	statusOut struct {
		// Repo is the repository root; Store whether a store could be loaded, and StoreError why not.
		// NewerStoreError says why the store .adamap/latest now names did not load, when the store
		// above is an earlier one still serving.
		Repo            string `json:"repo"`
		Store           bool   `json:"store"`
		StoreError      string `json:"store_error,omitempty"`
		NewerStoreError string `json:"newer_store_error,omitempty"`
		// Commit is the commit the store describes, Pinned whether --commit fixed it, StoreDirty
		// whether it was indexed with uncommitted changes.
		Commit     string        `json:"commit,omitempty"`
		Pinned     bool          `json:"pinned,omitempty"`
		StoreDirty bool          `json:"store_dirty,omitempty"`
		Indexer    *model.Tool   `json:"indexer,omitempty"`
		Created    string        `json:"created,omitempty"`
		Counts     *model.Counts `json:"counts,omitempty"`
		// Head is the repository's HEAD and Dirty whether tracked files have changes now; GitError
		// says why they are unknown.
		Head     string `json:"head,omitempty"`
		AtHead   bool   `json:"at_head"`
		Dirty    bool   `json:"dirty"`
		GitError string `json:"git_error,omitempty"`
		// Stale says the store is missing or does not describe the working tree; Refresh is the
		// command that rebuilds it.
		Stale   bool   `json:"stale"`
		Refresh string `json:"refresh,omitempty"`
	}
)

// text is a tool result whose content is s, with every line escaped as the CLI escapes store
// strings. The answers escape each store string already; this catches what reaches the text
// another way, such as an error.
func text(s string) *mcp.CallToolResult {
	return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: safeLines(s)}}}
}

// safeLines is s with each line that holds a control or format character, or invalid UTF-8, quoted
// by query.Safe. A line the answers built is printable already and stays as it is.
func safeLines(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = query.Safe(l)
	}
	return strings.Join(lines, "\n")
}

// toolError is err as the tool error the model reads, its lines escaped like text content.
func toolError(err error) error { return errors.New(safeLines(err.Error())) }

// note is a remark about an answer, such as that it is empty, on a line of its own: the CLI puts it
// on stderr, which a tool result does not have.
func note(b *strings.Builder, format string, a ...any) {
	fmt.Fprintf(b, "(%s)\n", fmt.Sprintf(format, a...))
}

// resolve finds the symbol spec means, as the CLI does, pointing a model at find_symbol.
func resolve(x *query.Index, spec string) (query.Symbol, error) {
	s, err := x.Resolve(spec)
	if errors.Is(err, query.ErrNoSymbol) {
		return s, fmt.Errorf("%w (find_symbol lists the names the index has)", err)
	}
	return s, err
}

func (s *server) mapTool(_ context.Context, _ *mcp.CallToolRequest, in mapIn) (*mcp.CallToolResult, mapOut, error) {
	switch {
	case in.Tokens < 0:
		return nil, mapOut{}, fmt.Errorf("tokens must be positive, not %d", in.Tokens)
	case in.Tokens == 0:
		in.Tokens = defaultTokens
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, mapOut{}, toolError(err)
	}
	words := rank.Words(in.For)
	r := rank.Rank(snap.idx, words)
	files := rank.Fit(r.Files, in.Tokens)
	var b strings.Builder
	b.WriteString(rank.Text(files))
	out := mapOut{Commit: snap.header.Commit, Tokens: in.Tokens, Estimate: rank.Tokens(b.String()), Matched: r.Matched, Files: []mapFile{}}
	if len(words) > 0 {
		out.For = words
	}
	for _, f := range files {
		mf := mapFile{Path: f.Path, Rank: f.Rank}
		for _, sym := range f.Symbols {
			mf.Symbols = append(mf.Symbols, mapSymbol{
				ID: sym.ID, Kind: rank.Label(sym), Name: sym.Name, Detail: rank.Detail(sym), Refs: sym.Refs, Score: sym.Score,
			})
		}
		out.Files = append(out.Files, mf)
	}
	switch {
	case len(words) > 0 && r.Matched == 0:
		note(&b, "nothing matches for %s; the map is not personalised", query.Safe(in.For))
	case len(r.Files) == 0:
		note(&b, "the store for %s defines no symbols to map", snap.header.Commit[:12])
	case len(files) == 0:
		note(&b, "nothing fits in %d tokens: raise tokens", in.Tokens)
	}
	return text(b.String()), out, nil
}

func (s *server) outline(_ context.Context, _ *mcp.CallToolRequest, in outlineIn) (*mcp.CallToolResult, outlineOut, error) {
	if in.File == "" {
		return nil, outlineOut{}, errors.New("file must not be empty")
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, outlineOut{}, toolError(err)
	}
	p, err := s.cfg.Path(in.File)
	if err != nil {
		return nil, outlineOut{}, toolError(err)
	}
	syms, err := snap.x.Outline(p)
	if errors.Is(err, query.ErrNoDocument) {
		return nil, outlineOut{}, fmt.Errorf("no document %q in the index of commit %s", p, snap.header.Commit[:12])
	}
	if err != nil {
		return nil, outlineOut{}, toolError(err)
	}
	var b strings.Builder
	for _, sym := range syms {
		b.WriteString(sym.Short() + "\n")
	}
	if len(syms) == 0 {
		note(&b, "no symbols defined in %s", query.Safe(p))
	}
	return text(b.String()), outlineOut{meta(snap), p, syms}, nil
}

func (s *server) find(_ context.Context, _ *mcp.CallToolRequest, in findIn) (*mcp.CallToolResult, findOut, error) {
	switch {
	case in.Name == "":
		return nil, findOut{}, errors.New("name must not be empty")
	case in.Limit < 0:
		return nil, findOut{}, fmt.Errorf("limit must be positive, not %d", in.Limit)
	case in.Limit == 0:
		in.Limit = defaultFindLimit
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, findOut{}, toolError(err)
	}
	res := snap.x.Find(in.Name, in.Limit)
	var b strings.Builder
	for _, sym := range res.Symbols {
		b.WriteString(sym.Short() + "\n")
	}
	switch more := res.Total - len(res.Symbols); {
	case res.Total == 0:
		note(&b, "no symbol matches %q", in.Name)
	case more == 1:
		note(&b, "1 more match; raise limit to see it")
	case more > 1:
		note(&b, "%d more matches; raise limit to see them", more)
	}
	return text(b.String()), findOut{meta(snap), res}, nil
}

func (s *server) refs(_ context.Context, _ *mcp.CallToolRequest, in symbolIn) (*mcp.CallToolResult, refsOut, error) {
	if in.Symbol == "" {
		return nil, refsOut{}, errors.New("symbol must not be empty")
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, refsOut{}, toolError(err)
	}
	sym, err := resolve(snap.x, in.Symbol)
	if err != nil {
		return nil, refsOut{}, toolError(err)
	}
	res, err := snap.x.Refs(sym.ID)
	if err != nil {
		return nil, refsOut{}, toolError(err)
	}
	var b strings.Builder
	for _, f := range res.Files {
		for _, site := range f.Refs {
			fmt.Fprintf(&b, "%s:%d %s %s\n", query.Safe(f.Path), site.Line, query.Safe(site.Kind), query.Safe(res.Symbol.Name))
		}
	}
	if res.Total == 0 {
		note(&b, "no occurrences of %s", query.Safe(res.Symbol.Name))
	}
	return text(b.String()), refsOut{meta(snap), res}, nil
}

func (s *server) callers(_ context.Context, _ *mcp.CallToolRequest, in symbolIn) (*mcp.CallToolResult, callersOut, error) {
	if in.Symbol == "" {
		return nil, callersOut{}, errors.New("symbol must not be empty")
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, callersOut{}, toolError(err)
	}
	sym, err := resolve(snap.x, in.Symbol)
	if err != nil {
		return nil, callersOut{}, toolError(err)
	}
	res, err := snap.x.Callers(sym.ID)
	if err != nil {
		return nil, callersOut{}, toolError(err)
	}
	var b strings.Builder
	for _, c := range res.Callers {
		lines := make([]string, len(c.Sites))
		for i, site := range c.Sites {
			lines[i] = strconv.Itoa(site.Line)
		}
		fmt.Fprintf(&b, "%s refs=%s\n", c.Symbol.Short(), strings.Join(lines, ","))
	}
	if len(res.Callers) == 0 {
		note(&b, "no callers of %s", query.Safe(res.Symbol.Name))
	}
	switch res.Unattributed {
	case 0:
	case 1:
		note(&b, "1 reference lies outside every definition, so it has no caller")
	default:
		note(&b, "%d references lie outside every definition, so they have no caller", res.Unattributed)
	}
	return text(b.String()), callersOut{meta(snap), res}, nil
}

func (s *server) impact(_ context.Context, _ *mcp.CallToolRequest, in impactIn) (*mcp.CallToolResult, impactOut, error) {
	switch {
	case in.Target == "":
		return nil, impactOut{}, errors.New("target must not be empty")
	case in.Depth < 0:
		return nil, impactOut{}, fmt.Errorf("depth must be at least 1, not %d", in.Depth)
	case in.Depth == 0:
		in.Depth = query.DefaultDepth
	}
	snap, err := s.stores.get()
	if err != nil {
		return nil, impactOut{}, toolError(err)
	}
	var (
		res    query.ImpactResult
		target string // what the note calls the target
	)
	// A path the index has a document for is a file; anything else is a symbol, as in the CLI.
	if p, perr := s.cfg.Path(in.Target); perr == nil && snap.x.HasDocument(p) {
		target = p
		res, err = snap.x.ImpactFile(p, in.Depth)
	} else {
		var sym query.Symbol
		if sym, err = resolve(snap.x, in.Target); err == nil {
			target = sym.Name
			res, err = snap.x.Impact(sym.ID, in.Depth)
		}
	}
	if err != nil {
		return nil, impactOut{}, toolError(err)
	}
	var b strings.Builder
	for _, i := range res.Impacted {
		fmt.Fprintf(&b, "%s depth=%d\n", i.Symbol.Short(), i.Depth)
	}
	if len(res.Impacted) == 0 {
		note(&b, "nothing depends on %s within depth %d", query.Safe(target), res.Depth)
	}
	return text(b.String()), impactOut{meta(snap), res}, nil
}

func (s *server) status(_ context.Context, _ *mcp.CallToolRequest, _ statusIn) (*mcp.CallToolResult, statusOut, error) {
	out := statusOut{Repo: s.repo, Pinned: s.cfg.Commit != "", Refresh: "adamap index --repo " + shellArg(s.repo)}
	var b strings.Builder
	snap, newerErr, err := s.stores.current()
	if err != nil {
		out.StoreError = err.Error()
		fmt.Fprintf(&b, "no store: %v\n", err)
	} else {
		h := snap.header
		out.Store, out.Commit, out.StoreDirty = true, h.Commit, h.Dirty
		out.Indexer, out.Counts = &h.Indexer, &h.Counts
		if !h.Created.IsZero() {
			out.Created = h.Created.UTC().Format(time.RFC3339)
		}
		fmt.Fprintf(&b, "store: %s", h.Commit[:12])
		if h.Dirty {
			b.WriteString(" (indexed with uncommitted changes)")
		}
		if out.Created != "" {
			fmt.Fprintf(&b, ", indexed %s", out.Created)
		}
		if h.Indexer.Name != "" {
			fmt.Fprintf(&b, " by %s", query.Safe(strings.TrimSpace(h.Indexer.Name+" "+h.Indexer.Version)))
		}
		fmt.Fprintf(&b, ": %d documents, %d symbols, %d occurrences\n", h.Counts.Documents, h.Counts.Symbols, h.Counts.Occurrences)
		if newerErr != nil {
			out.NewerStoreError = newerErr.Error()
			fmt.Fprintf(&b, "a newer store failed to load, so this one still serves: %v\n", newerErr)
		}
	}

	known := false
	if s.cfg.Head == nil {
		out.GitError = "git was not asked"
	} else if head, dirty, err := s.cfg.Head(); err != nil {
		out.GitError = err.Error()
	} else {
		known = true
		out.Head, out.Dirty = head, dirty
	}
	if known {
		state := "no uncommitted changes"
		if out.Dirty {
			state = "uncommitted changes in tracked files"
		}
		fmt.Fprintf(&b, "HEAD: %s, %s\n", short(out.Head), state)
	} else {
		fmt.Fprintf(&b, "HEAD: unknown (%s)\n", out.GitError)
	}

	// A store indexed from the dirty tree it is asked about is not called stale, since indexing
	// again could not clear that, and git does not say whether files changed since; maybe is that
	// case, where the refresh command is offered all the same.
	maybe := false
	switch {
	case !out.Store:
		out.Stale = true
	case !known:
		b.WriteString("cannot tell whether the store is current\n")
	default:
		out.AtHead = out.Commit == out.Head
		switch {
		case !out.AtHead:
			fmt.Fprintf(&b, "stale: the store describes %s, not HEAD\n", out.Commit[:12])
		case out.Dirty && out.StoreDirty:
			maybe = true
			b.WriteString("the store and the tree both have uncommitted changes: refresh if files changed since it was indexed\n")
		case out.Dirty:
			b.WriteString("stale: the working tree has uncommitted changes the store does not show\n")
		case out.StoreDirty:
			b.WriteString("stale: the store was indexed with uncommitted changes, and the tree has none now\n")
		case out.NewerStoreError == "":
			b.WriteString("the store is current\n")
		}
		out.Stale = !out.AtHead || out.Dirty != out.StoreDirty
		if maybe {
			fmt.Fprintf(&b, "refresh: %s\n", out.Refresh)
		}
	}
	// A store that did not load is newer than the one serving, whatever git says.
	out.Stale = out.Stale || out.NewerStoreError != ""
	if out.Stale {
		fmt.Fprintf(&b, "refresh: %s\n", out.Refresh)
		if out.Pinned {
			b.WriteString("this server is pinned to its commit by --commit: restart it without --commit to follow new stores\n")
		}
	} else if !maybe {
		out.Refresh = ""
	}
	return text(b.String()), out, nil
}

// short is a commit as people read it.
func short(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// shellArg is p as one argument of a shell command line: in double quotes when it holds a space.
// Backslashes stay as they are, since they are Windows' separators.
func shellArg(p string) string {
	if strings.ContainsAny(p, " \t") {
		return `"` + p + `"`
	}
	return p
}
