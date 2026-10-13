package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"path"
	"path/filepath"
	"slices"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/query"
	"github.com/cristiangirlea/adanext-map/internal/store"
)

// queryCmd is what the query commands (outline, find, refs, callers, impact) share: the flags
// --repo, --commit and --json, one argument, the store, and the output.
type queryCmd struct {
	name, arg, summary string
	fl                 *flag.FlagSet
	repo, commit       string
	asJSON             bool
	stdout, stderr     io.Writer

	header store.Header
	x      *query.Index
}

// newQueryCmd makes the command name, whose single argument is described by arg (such as "<file>").
// The command adds its own flags to c.fl before calling parse.
func newQueryCmd(name, arg, summary string, stdout, stderr io.Writer) *queryCmd {
	c := &queryCmd{name: name, arg: arg, summary: summary, stdout: stdout, stderr: stderr}
	c.fl = flag.NewFlagSet(name, flag.ContinueOnError)
	c.fl.SetOutput(io.Discard)
	c.fl.StringVar(&c.repo, "repo", ".", "the repository root, where .adamap lives")
	c.fl.StringVar(&c.commit, "commit", "", "the full hash of the commit to ask about (default: the one indexed last)")
	c.fl.BoolVar(&c.asJSON, "json", false, "print one line of JSON instead of text")
	return c
}

func (c *queryCmd) usage(w io.Writer) {
	fmt.Fprintf(w, "usage: adamap %s %s [flags]\n\n%s\n\n", c.name, c.arg, c.summary)
	c.fl.SetOutput(w)
	c.fl.PrintDefaults()
	c.fl.SetOutput(io.Discard)
}

// usageError reports a mistake in how the command was called: exit code 2.
func (c *queryCmd) usageError(err error) int {
	fmt.Fprintf(c.stderr, "adamap %s: %v\n\n", c.name, err)
	c.usage(c.stderr)
	return 2
}

// parse reads the flags and the one argument, which may come before, between or after the flags;
// everything after a bare -- is the argument. When done is true the command is over, with code:
// help was asked for, or the call was wrong.
func (c *queryCmd) parse(args []string) (arg string, code int, done bool) {
	flagArgs, after := args, []string(nil)
	if i := slices.Index(args, "--"); i >= 0 {
		flagArgs, after = args[:i], args[i+1:]
	}
	var positional []string
	for {
		if err := c.fl.Parse(flagArgs); err != nil {
			if errors.Is(err, flag.ErrHelp) {
				c.usage(c.stdout)
				return "", 0, true
			}
			return "", c.usageError(err), true
		}
		if c.fl.NArg() == 0 {
			break
		}
		positional = append(positional, c.fl.Arg(0))
		flagArgs = c.fl.Args()[1:]
	}
	positional = append(positional, after...)
	switch {
	case len(positional) == 0:
		return "", c.usageError(fmt.Errorf("missing %s", c.arg)), true
	case len(positional) > 1:
		return "", c.usageError(fmt.Errorf("unexpected argument %q", positional[1])), true
	case c.commit != "":
		if err := store.CheckCommit(c.commit); err != nil {
			return "", c.usageError(fmt.Errorf("--commit: %w", err)), true
		}
	}
	return positional[0], 0, false
}

// open loads the store of --commit, or of the commit indexed last, and indexes it for the queries.
func (c *queryCmd) open() error {
	commit := c.commit
	if commit == "" {
		var err error
		if commit, err = store.Latest(c.repo); err != nil {
			return err
		}
	}
	h, idx, err := store.Load(c.repo, commit)
	if err != nil {
		return err
	}
	c.header, c.x = h, query.New(idx)
	return nil
}

// short is the commit asked about, as far as people read it.
func (c *queryCmd) short() string { return c.header.Commit[:12] }

// fail reports err and gives the exit code for a command that failed: 1.
func (c *queryCmd) fail(err error) int {
	fmt.Fprintf(c.stderr, "adamap %s: %v\n", c.name, err)
	return 1
}

// note says something about the result on stderr, such as that it is empty. With --json it says
// nothing: the JSON is complete in itself.
func (c *queryCmd) note(format string, a ...any) {
	if !c.asJSON {
		fmt.Fprintf(c.stderr, "adamap %s: %s\n", c.name, fmt.Sprintf(format, a...))
	}
}

// meta heads every JSON answer: which commit it is about, and whether that store was dirty.
type meta struct {
	Commit string `json:"commit"`
	Dirty  bool   `json:"dirty,omitempty"`
}

func (c *queryCmd) meta() meta { return meta{c.header.Commit, c.header.Dirty} }

// emitJSON prints v as one line of JSON.
func (c *queryCmd) emitJSON(v any) int {
	if err := writeJSON(c.stdout, v, func(e *json.Encoder) { e.SetEscapeHTML(false) }); err != nil {
		return c.fail(err)
	}
	return 0
}

// writeJSON writes v and a newline to w as JSON, from an encoder that setup has configured.
// encoding/json escapes the C0 controls, but leaves DEL and the C1 controls (U+0080 to U+009F,
// among them the one-character CSI that a terminal reads as ESC [) as they are, so those become
// \u escapes here. Decoding gives the same values back. They can only be inside a string: that is
// the only place the encoder writes a character that is not ASCII graphic, and UTF-8 starts a C1
// control with the byte 0xC2.
func writeJSON(w io.Writer, v any, setup func(*json.Encoder)) error {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	setup(enc)
	if err := enc.Encode(v); err != nil {
		return err
	}
	in, out := b.Bytes(), make([]byte, 0, b.Len())
	for i := 0; i < len(in); i++ {
		switch {
		case in[i] == 0x7f:
			out = append(out, `\u007f`...)
		case in[i] == 0xc2 && i+1 < len(in) && in[i+1] >= 0x80 && in[i+1] <= 0x9f:
			out = fmt.Appendf(out, `\u%04x`, in[i+1])
			i++
		default:
			out = append(out, in[i])
		}
	}
	_, err := w.Write(out)
	return err
}

// resolve finds the symbol spec means: a symbol string, or a name that fits exactly one symbol.
func (c *queryCmd) resolve(spec string) (query.Symbol, error) {
	s, err := c.x.Resolve(spec)
	if errors.Is(err, query.ErrNoSymbol) {
		return s, fmt.Errorf("%w (`adamap find <name>` lists the names the index has)", err)
	}
	return s, err
}

// indexPath turns a path as the user typed it into the form the index keeps: slash-separated and
// relative to the repository root. A relative path is taken to be relative to the repository, as
// the index's own paths are; an absolute one must lie inside it.
func indexPath(repo, arg string) (string, error) {
	p := filepath.ToSlash(arg)
	if filepath.IsAbs(arg) {
		rel, ok := relativeTo(repo, arg)
		if !ok {
			return "", fmt.Errorf("%s is outside the repository %s", arg, repo)
		}
		p = filepath.ToSlash(rel)
	}
	return path.Clean(p), nil
}

// relativeTo is p relative to the directory dir, if p lies inside it. Both are compared as given
// and then with links resolved, since Windows may spell a temporary directory with a short name.
func relativeTo(dir, p string) (string, bool) {
	absDir, err1 := filepath.Abs(dir)
	absP, err2 := filepath.Abs(p)
	if err1 != nil || err2 != nil {
		return "", false
	}
	resolve := func(s string) string {
		if r, err := filepath.EvalSymlinks(s); err == nil {
			return r
		}
		return s
	}
	for _, pair := range [][2]string{{absDir, absP}, {resolve(absDir), resolve(absP)}} {
		rel, err := filepath.Rel(pair[0], pair[1])
		if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return rel, true
		}
	}
	return "", false
}
