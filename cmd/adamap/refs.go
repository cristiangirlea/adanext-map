package main

import (
	"fmt"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/query"
)

// runRefs is `adamap refs <symbol>`.
func runRefs(args []string, stdout, stderr io.Writer) int {
	c := newQueryCmd("refs", "<symbol>",
		"Lists every occurrence of a symbol, definitions first, grouped by file, one per line:\n"+
			"path:line kind name. The symbol is a SCIP symbol string or a name that fits exactly one symbol.",
		stdout, stderr)
	spec, code, done := c.parse(args)
	if done {
		return code
	}
	if err := c.open(); err != nil {
		return c.fail(err)
	}
	sym, err := c.resolve(spec)
	if err != nil {
		return c.fail(err)
	}
	res, err := c.x.Refs(sym.ID)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.emitJSON(struct {
			meta
			query.RefsResult
		}{c.meta(), res})
	}
	for _, f := range res.Files {
		for _, s := range f.Refs {
			fmt.Fprintf(stdout, "%s:%d %s %s\n", query.Safe(f.Path), s.Line, query.Safe(s.Kind), query.Safe(res.Symbol.Name))
		}
	}
	if res.Total == 0 {
		c.note("no occurrences of %s", query.Safe(res.Symbol.Name))
	}
	return 0
}
