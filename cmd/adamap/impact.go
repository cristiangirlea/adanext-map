package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/query"
)

// runImpact is `adamap impact <symbol|file>`.
func runImpact(args []string, stdout, stderr io.Writer) int {
	c := newQueryCmd("impact", "<symbol|file>",
		"Lists what depends on a symbol or a file through callers, transitively, nearest first, one per\n"+
			"line: path:line kind name depth=<calls away>. The argument is a path in the index, a SCIP\n"+
			"symbol string, or a name that fits exactly one symbol.", stdout, stderr)
	depth := c.fl.Int("depth", query.DefaultDepth, "follow callers at most this many calls away")
	arg, code, done := c.parse(args)
	if done {
		return code
	}
	if *depth < 1 {
		return c.usageError(fmt.Errorf("--depth must be at least 1, not %d", *depth))
	}
	if err := c.open(); err != nil {
		return c.fail(err)
	}
	var (
		res    query.ImpactResult
		target string // what the notes call the target
		err    error
	)
	// A path the index has a document for is a file; anything else is a symbol. An argument that is
	// neither, and is a path the index cannot hold, gets the path's error.
	if p, perr := indexPath(c.repo, arg); perr == nil && c.x.HasDocument(p) {
		target = p
		res, err = c.x.ImpactFile(p, *depth)
	} else {
		var sym query.Symbol
		if sym, err = c.resolve(arg); err == nil {
			target = sym.Name
			res, err = c.x.Impact(sym.ID, *depth)
		} else if perr != nil && errors.Is(err, query.ErrNoSymbol) {
			err = perr
		}
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.emitJSON(struct {
			meta
			query.ImpactResult
		}{c.meta(), res})
	}
	for _, i := range res.Impacted {
		fmt.Fprintf(stdout, "%s depth=%d\n", i.Symbol.Short(), i.Depth)
	}
	if len(res.Impacted) == 0 {
		c.note("nothing depends on %s within depth %d", query.Safe(target), res.Depth)
	}
	return 0
}
