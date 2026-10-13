package rank

import (
	"testing"
	"time"
)

func TestRankAndFitALargeIndexInUnderASecond(t *testing.T) {
	idx := synthetic(5000, 10, 40)
	n := idx.Counts()
	if n.Documents != 5000 || n.Occurrences != 200_000 {
		t.Fatalf("the synthetic index has %d documents and %d occurrences", n.Documents, n.Occurrences)
	}
	start := time.Now()
	text := Text(Fit(Rank(idx, Words("pkg7 Sym12")).Files, 1024))
	took := time.Since(start)
	t.Logf("ranked and rendered %d documents, %d occurrences in %v", n.Documents, n.Occurrences, took)
	if text == "" || Tokens(text) > 1024 {
		t.Fatalf("the map takes %d tokens: %q", Tokens(text), text)
	}
	if took > time.Second && !raceEnabled {
		t.Errorf("took %v, want under 1s", took)
	}
}
