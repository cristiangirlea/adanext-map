package store

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// endless is a JSON stream that never ends: prefix, then elem again and again. read counts the bytes
// handed out, so a test can tell a decoder that stopped early from one that read it all.
type endless struct {
	prefix, elem string
	off          int
	read         int64
}

func (e *endless) Read(p []byte) (int, error) {
	n := 0
	for n < len(p) {
		var c int
		if e.off < len(e.prefix) {
			c = copy(p[n:], e.prefix[e.off:])
		} else {
			c = copy(p[n:], e.elem[(e.off-len(e.prefix))%len(e.elem):])
		}
		n += c
		e.off += c
	}
	e.read += int64(n)
	return n, nil
}

func TestDecodeStopsAtTheCountLimitWithoutReadingTheRest(t *testing.T) {
	for _, tc := range []struct {
		name         string
		prefix, elem string
		lower        func(*model.Limits)
		want         string
	}{
		{"documents", `{"documents":[`, `{"path":"a.go"},`,
			func(l *model.Limits) { l.Documents = 100 }, "more than the limit of 100 documents"},
		{"global symbols", `{"symbols":[`, `{"id":"x"},`,
			func(l *model.Limits) { l.Symbols = 100 }, "more than the limit of 100 symbols"},
		{"local symbols in one document", `{"documents":[{"path":"a.go","locals":[`, `{"id":"local 0"},`,
			func(l *model.Limits) { l.Symbols = 100 }, "more than the limit of 100 symbols"},
		{"occurrences in one document", `{"documents":[{"path":"a.go","occurrences":[`, `{},`,
			func(l *model.Limits) { l.Occurrences = 100 }, "more than the limit of 100 occurrences"},
		{"relationships of one symbol", `{"symbols":[{"id":"x","relationships":[`, `{},`,
			func(l *model.Limits) { l.Relationships = 100 }, "more than the limit of 100 relationships"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lim := model.DefaultLimits()
			tc.lower(&lim)
			r := &endless{prefix: tc.prefix, elem: tc.elem}
			_, err := decodeIndex(r, lim)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("decodeIndex err = %v, want one containing %q", err, tc.want)
			}
			if r.read > 1<<20 {
				t.Errorf("read %d bytes of an endless stream before giving up, want well under 1 MiB", r.read)
			}
		})
	}
}

func TestDecodeGivesWhatEncodingJSONGives(t *testing.T) {
	stored, err := json.Marshal(fixture(t))
	if err != nil {
		t.Fatal(err)
	}
	for name, doc := range map[string]string{
		"a store":                 string(stored),
		"empty and null lists":    `{"tool":{"name":"t"},"documents":[],"symbols":null}`,
		"null elements":           `{"documents":[null,{"path":"a.go","locals":[null],"occurrences":[null]}],"symbols":[null]}`,
		"unknown fields skipped":  `{"future":{"a":[1,{"b":null}],"c":"d"},"documents":[{"path":"a.go","x":[[]],"locals":[{"id":"l","y":{}}]}],"z":1}`,
		"relationships and a def": `{"symbols":[{"id":"s","definition":{"path":"a.go","range":{"start_line":1}},"relationships":[{"symbol":"t","reference":true}]}]}`,
		"an empty object":         `{}`,
		"a null store":            `null`,
	} {
		t.Run(name, func(t *testing.T) {
			var want model.Index
			if err := json.Unmarshal([]byte(doc), &want); err != nil {
				t.Fatal(err)
			}
			got, err := decodeIndex(strings.NewReader(doc), model.DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(*got, want) {
				t.Errorf("decodeIndex = %+v\nwant %+v", *got, want)
			}
		})
	}
}

func TestDecodeRefusesWhatEncodingJSONRefuses(t *testing.T) {
	for _, doc := range []string{
		``, `[`, `[]`, `7`, `{"documents":7}`, `{"documents":{}}`, `{"documents":[7]}`, `{"documents":[{"path":7}]}`,
		`{"documents":[{"path":"a.go"}`, `{"documents":[{"path":"a.go"},`, `{"symbols":[{"id":"s","relationships":[1]}]}`,
		`{"documents":[{"locals":[{"id":"l"`, `{"tool":}`,
	} {
		if _, err := decodeIndex(strings.NewReader(doc), model.DefaultLimits()); err == nil {
			t.Errorf("decodeIndex(%q) was accepted", doc)
		}
	}
}

func relationships(idx *model.Index) int {
	n := 0
	for _, s := range idx.Symbols {
		n += len(s.Relationships)
	}
	for _, d := range idx.Documents {
		for _, s := range d.Locals {
			n += len(s.Relationships)
		}
	}
	return n
}

func TestLoadRefusesAStoreOverAnyCountLimitNamingIt(t *testing.T) {
	idx := fixture(t)
	c := idx.Counts()
	if relationships(idx) == 0 {
		t.Fatal("the fixture has no relationships to limit")
	}
	for _, tc := range []struct {
		lower func(*model.Limits)
		want  string
	}{
		{func(l *model.Limits) { l.Documents = c.Documents - 1 }, "more than the limit of 1 documents"},
		{func(l *model.Limits) { l.Symbols = c.Symbols - 1 }, "symbols"},
		{func(l *model.Limits) { l.Occurrences = c.Occurrences - 1 }, "occurrences"},
		{func(l *model.Limits) { l.Relationships = relationships(idx) - 1 }, "relationships"},
	} {
		repo := t.TempDir()
		if _, err := Write(repo, header(commitA), idx); err != nil {
			t.Fatal(err)
		}
		// The header is honest here; the stream is counted too, and refused all the same.
		lim := model.DefaultLimits()
		tc.lower(&lim)
		if _, _, err := load(repo, commitA, MaxLoadBytes, lim); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("load err = %v, want one containing %q", err, tc.want)
		}
		if _, _, err := load(repo, commitA, MaxLoadBytes, model.DefaultLimits()); err != nil {
			t.Errorf("load under the default limits: %v", err)
		}
	}
}

func TestLoadChecksTheHeaderCountsBeforeOpeningTheStore(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	// Without its store file, only the header can be what refuses it.
	if err := os.Remove(filepath.Join(repo, ".adamap", commitA, "store.json.gz")); err != nil {
		t.Fatal(err)
	}
	lim := model.DefaultLimits()
	lim.Occurrences = 1
	if _, _, err := load(repo, commitA, MaxLoadBytes, lim); err == nil || !strings.Contains(err.Error(), "more than the limit of 1 occurrences") {
		t.Fatalf("load err = %v, want the header's occurrence count refused", err)
	}
}

func TestLoadDoesNotTrustHeaderCounts(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) {
		h["counts"] = map[string]any{"documents": 0, "symbols": 0, "occurrences": 0}
	})
	lim := model.DefaultLimits()
	lim.Documents = 1
	if _, _, err := load(repo, commitA, MaxLoadBytes, lim); err == nil || !strings.Contains(err.Error(), "more than the limit of 1 documents") {
		t.Fatalf("load err = %v, want the streamed document count refused", err)
	}
}

func TestDecodeRefusesAnUnknownFieldNestedTooDeepWithoutReadingTheRest(t *testing.T) {
	for _, open := range []string{"[", `{"a":`} {
		// The LimitReader only keeps a decoder without the depth limit from running away.
		r := &endless{prefix: `{"x":`, elem: open}
		_, err := decodeIndex(io.LimitReader(r, 64<<20), model.DefaultLimits())
		if err == nil || !strings.Contains(err.Error(), "exceeded max depth") {
			t.Fatalf("open %q: decodeIndex err = %v, want one naming the depth limit", open, err)
		}
		if r.read > 1<<20 {
			t.Errorf("open %q: read %d bytes before giving up, want well under 1 MiB", open, r.read)
		}
	}
}

func TestDecodeSkipsAnUnknownFieldAtTheDepthLimit(t *testing.T) {
	n := 10000
	doc := `{"x":` + strings.Repeat("[", n) + strings.Repeat("]", n) + `}`
	if _, err := decodeIndex(strings.NewReader(doc), model.DefaultLimits()); err != nil {
		t.Fatalf("a value nested %d deep: %v", n, err)
	}
}
