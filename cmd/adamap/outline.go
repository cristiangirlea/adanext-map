package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/query"
)

// runOutline is `adamap outline <file>`: exit 0 on success, 1 when the question cannot be answered,
// 2 for a usage error.
func runOutline(args []string, stdout, stderr io.Writer) int {
	c := newQueryCmd("outline", "<file>",
		"Lists the symbols a file defines, by line, one per line: path:line kind name.", stdout, stderr)
	arg, code, done := c.parse(args)
	if done {
		return code
	}
	if err := c.open(); err != nil {
		return c.fail(err)
	}
	p, err := indexPath(c.repo, arg)
	if err != nil {
		return c.fail(err)
	}
	syms, err := c.x.Outline(p)
	if errors.Is(err, query.ErrNoDocument) {
		return c.fail(fmt.Errorf("no document %q in the index of commit %s", p, c.short()))
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.emitJSON(struct {
			meta
			Path    string         `json:"path"`
			Symbols []query.Symbol `json:"symbols"`
		}{c.meta(), p, syms})
	}
	for _, s := range syms {
		fmt.Fprintln(stdout, s.Short())
	}
	if len(syms) == 0 {
		c.note("no symbols defined in %s", query.Safe(p))
	}
	return 0
}
