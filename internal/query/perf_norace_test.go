//go:build !race

package query

// enforceBound is whether the timing test fails a query that is over its bound. The race detector
// makes code several times slower, so a run with -race only checks the answers.
const enforceBound = true
