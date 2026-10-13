package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// bigIndex is a synthetic index of n documents, each with a local symbol, two occurrences and a
// global symbol it defines. The symbols are Rust-like, full of <, > and &.
func bigIndex(n int) *model.Index {
	idx := &model.Index{Tool: model.Tool{Name: "rust-analyzer", Version: "0.3.2"}, ProjectRoot: "file:///work/m"}
	for i := range n {
		path := fmt.Sprintf("src/m%05d.rs", i)
		id := fmt.Sprintf("rust-analyzer cargo m 0.1.0 m%05d/Stack<T>#push().", i)
		r := model.Range{StartLine: i, StartCol: 4, EndLine: i, EndCol: 8}
		idx.Documents = append(idx.Documents, model.Document{
			Path:     path,
			Language: "rust",
			Locals:   []model.Symbol{{ID: "local 0", DisplayName: "x", Kind: "Variable"}},
			Occurrences: []model.Occurrence{
				{Range: r, Symbol: id, Roles: model.RoleDefinition, Enclosing: &model.Range{StartLine: i, EndLine: i + 3, EndCol: 1}},
				{Range: model.Range{StartLine: i + 1, StartCol: 8, EndLine: i + 1, EndCol: 9}, Symbol: "local 0", Roles: model.RoleReference | model.RoleRead},
			},
		})
		idx.Symbols = append(idx.Symbols, model.Symbol{
			ID:            id,
			DisplayName:   "push",
			Kind:          "Method",
			Doc:           "Pushes x & returns a <T> reference.",
			Signature:     "fn push(&mut self, x: T) -> Option<&T>",
			Definition:    &model.Location{Path: path, Range: r},
			Relationships: []model.Relationship{{Symbol: "rust-analyzer cargo m 0.1.0 Push<T>#push().", Implementation: true}},
		})
	}
	return idx
}

// encodingJSON is the store's JSON as encoding/json gives it for the whole index at once, with
// <, > and & left as they are.
func encodingJSON(t *testing.T, idx *model.Index) []byte {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(idx); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestStoreJSONIsEncodingJSONsWithoutHTMLEscaping(t *testing.T) {
	for _, tc := range []struct {
		name string
		idx  *model.Index
	}{
		{"empty", &model.Index{}},
		{"empty but not nil", &model.Index{Documents: []model.Document{}, Symbols: []model.Symbol{}}},
		{"no project root", &model.Index{Tool: model.Tool{Name: "scip-go"}, Documents: []model.Document{{Path: "a.go"}}}},
		{"fixture", fixture(t)},
		{"synthetic", bigIndex(200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := encodeIndex(&buf, tc.idx); err != nil {
				t.Fatal(err)
			}
			if want := encodingJSON(t, tc.idx); !bytes.Equal(buf.Bytes(), want) {
				t.Errorf("store JSON differs from encoding/json's:\n got %.300s\nwant %.300s", buf.Bytes(), want)
			}
		})
	}
}

func TestStoreJSONDecodesToTheSameModelEitherWay(t *testing.T) {
	for _, tc := range []struct {
		name string
		idx  *model.Index
	}{
		{"fixture", fixture(t)},
		{"synthetic", bigIndex(200)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := encodeIndex(&buf, tc.idx); err != nil {
				t.Fatal(err)
			}
			var plain model.Index
			if err := json.Unmarshal(buf.Bytes(), &plain); err != nil {
				t.Fatalf("json.Unmarshal: %v", err)
			}
			if !reflect.DeepEqual(&plain, tc.idx) {
				t.Errorf("json.Unmarshal gives another model")
			}
			streamed, err := decodeIndex(&buf, model.DefaultLimits())
			if err != nil {
				t.Fatalf("decodeIndex: %v", err)
			}
			if !reflect.DeepEqual(streamed, tc.idx) {
				t.Errorf("decodeIndex gives another model")
			}
		})
	}
}

// storeJSON is commit's store.json.gz in repo, decompressed.
func storeJSON(t *testing.T, repo, commit string) []byte {
	t.Helper()
	f, err := os.Open(filepath.Join(Dir(repo, commit), storeFile))
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatal(err)
	}
	data, err := io.ReadAll(gz)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestWriteThenLoadALargeIndexGivesBackTheSame(t *testing.T) {
	repo := t.TempDir()
	idx := bigIndex(5000)
	if _, err := Write(repo, header(commitA), idx); err != nil {
		t.Fatal(err)
	}
	_, got, err := Load(repo, commitA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, idx) {
		t.Errorf("loaded model differs from the written one")
	}
	var plain model.Index
	if err := json.Unmarshal(storeJSON(t, repo, commitA), &plain); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	if !reflect.DeepEqual(&plain, idx) {
		t.Errorf("json.Unmarshal of the store gives another model")
	}
}

func TestWriteLeavesHTMLCharactersUnescaped(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), bigIndex(1)); err != nil {
		t.Fatal(err)
	}
	data := string(storeJSON(t, repo, commitA))
	for _, want := range []string{`Stack<T>#push()`, `-> Option<&T>`, `x & returns`} {
		if !strings.Contains(data, want) {
			t.Errorf("store JSON lacks %q as it is:\n%s", want, data)
		}
	}
	for _, escaped := range []string{`\u003c`, `\u003e`, `\u0026`} {
		if strings.Contains(data, escaped) {
			t.Errorf("store JSON holds %s:\n%s", escaped, data)
		}
	}
}

func TestAStoreWrittenWithHTMLEscapingStillLoads(t *testing.T) {
	repo := t.TempDir()
	idx := bigIndex(10)
	if _, err := Write(repo, header(commitA), idx); err != nil {
		t.Fatal(err)
	}
	// How Write encoded the store before it streamed: the whole index at once, <>& escaped.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	if err := json.NewEncoder(gz).Encode(idx); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(Dir(repo, commitA), storeFile), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	_, got, err := Load(repo, commitA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, idx) {
		t.Errorf("loaded model differs from the written one")
	}
}

// writeSizes counts the bytes written to it and remembers the largest single write.
type writeSizes struct{ total, largest int }

func (w *writeSizes) Write(p []byte) (int, error) {
	w.total += len(p)
	w.largest = max(w.largest, len(p))
	return len(p), nil
}

func TestEncodeHoldsOneElementAtATime(t *testing.T) {
	idx := bigIndex(20000)
	var w writeSizes
	if err := encodeIndex(&w, idx); err != nil {
		t.Fatal(err)
	}
	// One document or symbol here is some 400 bytes; the whole JSON is megabytes.
	if w.largest > 4096 {
		t.Errorf("largest write is %d bytes of %d: the encoder holds more than one element", w.largest, w.total)
	}
	// What encoding the store allocates, with a generous bound: building the whole JSON in memory
	// takes at least its size.
	if !checkAllocations {
		return
	}
	runtime.GC()
	runtime.GC() // twice, to empty sync.Pool, where an earlier encoding can leave a large buffer
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	if err := encodeIndex(io.Discard, idx); err != nil {
		t.Fatal(err)
	}
	runtime.ReadMemStats(&after)
	if alloc := after.TotalAlloc - before.TotalAlloc; alloc > uint64(w.total/4) {
		t.Errorf("encoding %d bytes of JSON allocated %d bytes: more than a quarter of it", w.total, alloc)
	}
}

// failAfter accepts n bytes and then fails every write, as a full disk would.
type failAfter struct {
	n      int
	writes int // writes asked for after the first failure
	failed bool
}

var errDiskFull = errors.New("disk full")

func (w *failAfter) Write(p []byte) (int, error) {
	if w.failed {
		w.writes++
		return 0, errDiskFull
	}
	if len(p) > w.n {
		w.failed = true
		return 0, errDiskFull
	}
	w.n -= len(p)
	return len(p), nil
}

// A write that fails part way through the store is the error encodeIndex returns, and nothing is
// written after it: Write then never completes a store from a stream that broke.
func TestEncodeStopsAtTheFirstFailedWrite(t *testing.T) {
	for _, n := range []int{0, 10, 1000, 20000} {
		w := &failAfter{n: n}
		if err := encodeIndex(w, bigIndex(50)); !errors.Is(err, errDiskFull) {
			t.Fatalf("after %d bytes: err = %v, want the write's error", n, err)
		}
		if w.writes != 0 {
			t.Errorf("after %d bytes: %d writes after the failure", n, w.writes)
		}
	}
}
