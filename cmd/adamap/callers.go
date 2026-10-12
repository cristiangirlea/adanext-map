package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/cristiangirlea/adanext-map/internal/query"
)

// runCallers is `adamap callers <symbol>`.
func runCallers(args []string, stdout, stderr io.Writer) int {
	c := newQueryCmd("callers", "<symbol>",
		"Lists the symbols whose definition holds a reference to a symbol, one per line:\n"+
			"path:line kind name refs=<lines of the references>. The symbol is a SCIP symbol string or a\n"+
			"name that fits exactly one symbol.", stdout, stderr)
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
	res, err := c.x.Callers(sym.ID)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.emitJSON(struct {
			meta
			query.CallersResult
		}{c.meta(), res})
	}
	for _, caller := range res.Callers {
		lines := make([]string, len(caller.Sites))
		for i, s := range caller.Sites {
			lines[i] = strconv.Itoa(s.Line)
		}
		fmt.Fprintf(stdout, "%s refs=%s\n", caller.Symbol.Short(), strings.Join(lines, ","))
	}
	if len(res.Callers) == 0 {
		c.note("no callers of %s", res.Symbol.Name)
	}
	switch res.Unattributed {
	case 0:
	case 1:
		c.note("1 reference lies outside every definition, so it has no caller")
	default:
		c.note("%d references lie outside every definition, so they have no caller", res.Unattributed)
	}
	return 0
}
