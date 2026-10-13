package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/rank"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// mapJSON is the map as `adamap map --json` prints it (docs/map.md).
type mapJSON struct {
	Commit string `json:"commit"`
	// Tokens is the budget; Estimate is the text form's tokens, bytes / 4 rounded up.
	Tokens   int        `json:"tokens"`
	Estimate int        `json:"estimate"`
	For      []string   `json:"for,omitempty"`
	Matched  int        `json:"matched,omitempty"`
	Files    []fileJSON `json:"files"`
}

type fileJSON struct {
	Path    string       `json:"path"`
	Rank    float64      `json:"rank"`
	Symbols []symbolJSON `json:"symbols"`
}

type symbolJSON struct {
	ID string `json:"id"`
	// Kind and Detail are the label and the text after it on the symbol's line.
	Kind   string  `json:"kind"`
	Name   string  `json:"name"`
	Detail string  `json:"detail"`
	Refs   int     `json:"refs"`
	Score  float64 `json:"score"`
}

func mapUsage(w io.Writer, fl *flag.FlagSet) {
	fmt.Fprint(w, `usage: adamap map [--tokens n] [--for words] [--repo dir] [--commit sha] [--json]

Prints the files and symbols that matter most, ranked by how the code references them, in at most
--tokens tokens (estimated as bytes / 4). --for ranks the files that match its words higher.

`)
	fl.SetOutput(w)
	fl.PrintDefaults()
}

// runMap is `adamap map`: exit 0 on success, 1 when the store cannot be read, 2 for a usage error.
func runMap(args []string, stdout, stderr io.Writer) int {
	fl := flag.NewFlagSet("map", flag.ContinueOnError)
	fl.SetOutput(io.Discard)
	budget := fl.Int("tokens", 1024, "the most tokens the map takes, estimated as bytes / 4")
	words := fl.String("for", "", "words of the task: files whose path or symbols contain one rank higher")
	repo := fl.String("repo", ".", "the repository root, whose .adamap directory holds the store")
	commit := fl.String("commit", "", "the full hash of the commit to map (default: the store written last)")
	asJSON := fl.Bool("json", false, "print the map as JSON")
	usageError := func(err error) int {
		fmt.Fprintf(stderr, "adamap map: %v\n\n", err)
		mapUsage(stderr, fl)
		return 2
	}
	if err := fl.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			mapUsage(stdout, fl)
			return 0
		}
		return usageError(err)
	}
	switch {
	case fl.NArg() > 0:
		return usageError(fmt.Errorf("unexpected argument %q", fl.Arg(0)))
	case *budget <= 0:
		return usageError(fmt.Errorf("--tokens must be positive, not %d", *budget))
	case *commit != "":
		if err := store.CheckCommit(*commit); err != nil {
			return usageError(fmt.Errorf("--commit: %w", err))
		}
	}

	fail := func(err error) int {
		fmt.Fprintf(stderr, "adamap map: %v\n", err)
		return 1
	}
	c := *commit
	if c == "" {
		var err error
		if c, err = store.Latest(*repo); err != nil {
			return fail(err)
		}
	}
	_, idx, err := store.Load(*repo, c)
	if err != nil {
		return fail(err)
	}
	w := rank.Words(*words)
	r := rank.Rank(idx, w)
	files := rank.Fit(r.Files, *budget)
	text := rank.Text(files)
	switch {
	case len(w) > 0 && r.Matched == 0:
		fmt.Fprintf(stderr, "adamap map: nothing matches --for %q; the map is not personalised\n", *words)
	case len(r.Files) == 0:
		fmt.Fprintf(stderr, "adamap map: the store for %s defines no symbols to map\n", c[:12])
	case len(files) == 0:
		fmt.Fprintf(stderr, "adamap map: nothing fits in %d tokens: raise --tokens\n", *budget)
	}
	if !*asJSON {
		if _, err := io.WriteString(stdout, text); err != nil {
			return fail(err)
		}
		return 0
	}
	out := mapJSON{Commit: c, Tokens: *budget, Estimate: rank.Tokens(text), Matched: r.Matched, Files: []fileJSON{}}
	if len(w) > 0 {
		out.For = w
	}
	for _, f := range files {
		fj := fileJSON{Path: f.Path, Rank: f.Rank}
		for _, s := range f.Symbols {
			fj.Symbols = append(fj.Symbols, symbolJSON{
				ID: s.ID, Kind: rank.Label(s), Name: s.Name, Detail: rank.Detail(s), Refs: s.Refs, Score: s.Score,
			})
		}
		out.Files = append(out.Files, fj)
	}
	if err := writeJSON(stdout, out, func(e *json.Encoder) { e.SetIndent("", "  ") }); err != nil {
		return fail(err)
	}
	return 0
}
