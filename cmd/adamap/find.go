package main

import (
	"fmt"
	"io"
)

// defaultFindLimit is how many matches `adamap find` prints unless --limit says otherwise.
const defaultFindLimit = 25

// runFind is `adamap find <name>`.
func runFind(args []string, stdout, stderr io.Writer) int {
	c := newQueryCmd("find", "<name>",
		"Lists the symbols whose name fits: equal, equal ignoring case, then starting with it, then\n"+
			"containing it, one per line: path:line kind name.", stdout, stderr)
	limit := c.fl.Int("limit", defaultFindLimit, "print at most this many matches")
	name, code, done := c.parse(args)
	if done {
		return code
	}
	if *limit < 1 {
		return c.usageError(fmt.Errorf("--limit must be positive, not %d", *limit))
	}
	if err := c.open(); err != nil {
		return c.fail(err)
	}
	res := c.x.Find(name, *limit)
	if c.asJSON {
		return c.emitJSON(struct {
			meta
			Query   string `json:"query"`
			Total   int    `json:"total"`
			Symbols any    `json:"symbols"`
		}{c.meta(), res.Query, res.Total, res.Symbols})
	}
	for _, s := range res.Symbols {
		fmt.Fprintln(stdout, s.Short())
	}
	switch {
	case res.Total == 0:
		c.note("no symbol matches %q", name)
	case res.Total > len(res.Symbols):
		c.note("%d more matches; raise --limit to see them", res.Total-len(res.Symbols))
	}
	return 0
}
