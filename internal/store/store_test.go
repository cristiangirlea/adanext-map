package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

const (
	commitA = "0123456789abcdef0123456789abcdef01234567"
	commitB = "89abcdef0123456789abcdef0123456789abcdef"
)

func fixture(t *testing.T) *model.Index {
	t.Helper()
	idx, err := scip.Convert(sciptest.Index())
	if err != nil {
		t.Fatal(err)
	}
	return idx
}

func header(commit string) Header {
	return Header{
		Adamap:      "0.0.1-test",
		Commit:      commit,
		Dirty:       true,
		Indexer:     model.Tool{Name: "scip-go", Version: "v0.2.7"},
		ProjectRoot: "file:///work/m",
		Created:     time.Date(2026, 10, 13, 9, 30, 0, 0, time.UTC),
	}
}

func names(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, e := range entries {
		out = append(out, e.Name())
	}
	return out
}

func TestWriteThenLoadGivesBackTheSame(t *testing.T) {
	repo := t.TempDir()
	idx := fixture(t)
	dir, err := Write(repo, header(commitA), idx)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(repo, ".adamap", commitA); dir != want {
		t.Errorf("dir = %s, want %s", dir, want)
	}
	h, got, err := Load(repo, commitA)
	if err != nil {
		t.Fatal(err)
	}
	want := header(commitA)
	want.Format = FormatVersion
	want.Counts = model.Counts{Documents: 2, Symbols: 7, Occurrences: 9}
	if !reflect.DeepEqual(h, want) {
		t.Errorf("header = %+v, want %+v", h, want)
	}
	if !reflect.DeepEqual(got, idx) {
		t.Errorf("loaded model differs from the written one")
	}
}

func TestHeaderIsPlainJSONWithTheFormat(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(repo, ".adamap", commitA, "header.json"))
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatal(err)
	}
	if h["format"] != float64(1) || h["commit"] != commitA || h["dirty"] != true {
		t.Errorf("header.json = %s", data)
	}
}

func TestWriteLeavesNoTempFiles(t *testing.T) {
	repo := t.TempDir()
	for range 2 { // the second write replaces the first
		if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
			t.Fatal(err)
		}
	}
	if got, want := names(t, filepath.Join(repo, ".adamap", commitA)), []string{"header.json", "store.json.gz"}; !slices.Equal(got, want) {
		t.Errorf("store dir holds %v, want %v", got, want)
	}
	if got, want := names(t, filepath.Join(repo, ".adamap")), []string{commitA, "latest"}; !slices.Equal(got, want) {
		t.Errorf(".adamap holds %v, want %v", got, want)
	}
}

func TestLatestIsTheCommitWrittenLast(t *testing.T) {
	repo := t.TempDir()
	if _, err := Latest(repo); err == nil {
		t.Error("Latest of an empty repo gave no error")
	}
	for _, c := range []string{commitA, commitB} {
		if _, err := Write(repo, header(c), fixture(t)); err != nil {
			t.Fatal(err)
		}
	}
	if got, err := Latest(repo); err != nil || got != commitB {
		t.Errorf("Latest = %q, %v; want %s", got, err, commitB)
	}
}

func rewriteHeader(t *testing.T, repo string, change func(h map[string]any)) {
	t.Helper()
	path := filepath.Join(repo, ".adamap", commitA, "header.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var h map[string]any
	if err := json.Unmarshal(data, &h); err != nil {
		t.Fatal(err)
	}
	change(h)
	if data, err = json.Marshal(h); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadRefusesANewerFormatNamingTheAdamapNeeded(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) { h["format"] = 2; h["adamap"] = "0.9.0" })
	_, _, err := Load(repo, commitA)
	if err == nil || !strings.Contains(err.Error(), "format 2") || !strings.Contains(err.Error(), "adamap 0.9.0 or later") {
		t.Fatalf("err = %v, want one naming format 2 and adamap 0.9.0 or later", err)
	}
}

func TestLoadRefusesANewerFormatWithoutAVersionSayingANewerAdamap(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) { h["format"] = 2; delete(h, "adamap") })
	_, _, err := Load(repo, commitA)
	if err == nil || !strings.Contains(err.Error(), "use a newer adamap") {
		t.Fatalf("err = %v, want one saying to use a newer adamap", err)
	}
}

func TestLoadRefusesAHeaderOfAnotherCommit(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) { h["commit"] = commitB })
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), commitB) {
		t.Fatalf("err = %v, want one naming the commit the header claims", err)
	}
}

func TestAStoreWithoutItsHeaderDoesNotLoad(t *testing.T) {
	// A write cut short after store.json.gz leaves no header.json.
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(repo, ".adamap", commitA, "header.json")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "adamap index") {
		t.Fatalf("err = %v, want one saying to run adamap index", err)
	}
}

func TestLatestRefusesAFileThatIsNotACommit(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".adamap"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".adamap", "latest"), []byte("../../etc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Latest(repo); err == nil {
		t.Fatalf("Latest = %q, want an error", c)
	}
}

func TestLoadRefusesAHeaderWithoutAFormat(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) { delete(h, "format") })
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "adamap index") {
		t.Fatalf("err = %v, want one saying to run adamap index", err)
	}
}

func TestLoadOfAMissingStoreSaysToIndex(t *testing.T) {
	_, _, err := Load(t.TempDir(), commitA)
	if err == nil || !strings.Contains(err.Error(), "adamap index") {
		t.Fatalf("err = %v, want one saying to run adamap index", err)
	}
}

func TestCommitMustBeAFullHash(t *testing.T) {
	for _, c := range []string{commitA, strings.Repeat("ab", 32)} {
		if err := CheckCommit(c); err != nil {
			t.Errorf("CheckCommit(%q) = %v", c, err)
		}
	}
	for _, c := range []string{"", "0123456", strings.ToUpper(commitA), "../" + commitA[3:], commitA + "0", strings.Repeat("g", 40)} {
		if err := CheckCommit(c); err == nil {
			t.Errorf("CheckCommit(%q) accepted it", c)
		}
	}
	repo := t.TempDir()
	if _, err := Write(repo, header("../../escape"), fixture(t)); err == nil {
		t.Error("Write accepted a commit that is a path")
	}
	if _, _, err := Load(repo, "../../escape"); err == nil {
		t.Error("Load accepted a commit that is a path")
	}
}

func symlinkOrSkip(t *testing.T, target, link string) {
	t.Helper()
	if err := os.Symlink(target, link); err != nil {
		t.Skipf("cannot create a symlink here: %v", err)
	}
}

func TestStoreRefusesASymlinkedStoreDirectory(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	symlinkOrSkip(t, outside, filepath.Join(repo, ".adamap"))
	if _, err := Write(repo, header(commitA), fixture(t)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Write err = %v, want a refusal naming the symbolic link", err)
	}
	if got := names(t, outside); len(got) != 0 {
		t.Errorf("Write put %v outside the repository", got)
	}
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Load err = %v, want a refusal naming the symbolic link", err)
	}
}

func TestStoreRefusesASymlinkedCommitDirectory(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".adamap"), 0o755); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(repo, ".adamap", commitA))
	if _, err := Write(repo, header(commitA), fixture(t)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Write err = %v, want a refusal naming the symbolic link", err)
	}
	if got := names(t, outside); len(got) != 0 {
		t.Errorf("Write put %v outside the repository", got)
	}
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Load err = %v, want a refusal naming the symbolic link", err)
	}
}

func TestLoadStopsDecompressingAtTheLimit(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	// A planted store.json.gz: a few KiB of gzip that inflate to a megabyte of spaces before the JSON.
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	gz.Write(bytes.Repeat([]byte(" "), 1<<20))
	gz.Write([]byte("{}"))
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, ".adamap", commitA, "store.json.gz"), buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadLimited(repo, commitA, 1<<10); err == nil || !strings.Contains(err.Error(), "1024 bytes") {
		t.Fatalf("LoadLimited err = %v, want one naming the 1024 byte limit", err)
	}
	if _, _, err := LoadLimited(repo, commitA, 2<<20); err != nil {
		t.Errorf("LoadLimited under the limit: %v", err)
	}
}

func TestLoadRefusesADocumentPathOutsideTheProject(t *testing.T) {
	for _, p := range []string{"/etc/passwd", `C:\x`, "../../x", "a/../../x", ""} {
		repo := t.TempDir()
		idx := fixture(t)
		idx.Documents[0].Path = p // as a hand-edited store would have it; Convert would refuse it
		if _, err := Write(repo, header(commitA), idx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "document 0") {
			t.Errorf("path %q: Load err = %v, want one naming document 0", p, err)
		}
	}
}

func TestWritingACommitAgainReplacesItsStore(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	second := fixture(t)
	second.Documents = second.Documents[:1]
	if _, err := Write(repo, header(commitA), second); err != nil {
		t.Fatal(err)
	}
	h, got, err := Load(repo, commitA)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, second) {
		t.Errorf("Load returned %d documents, want the second write's %d", len(got.Documents), len(second.Documents))
	}
	if h.Counts != second.Counts() {
		t.Errorf("header counts = %+v, want %+v", h.Counts, second.Counts())
	}
	if got, want := names(t, filepath.Join(repo, ".adamap", commitA)), []string{"header.json", "store.json.gz"}; !slices.Equal(got, want) {
		t.Errorf("store dir holds %v, want %v", got, want)
	}
}

func TestLoadRefusesASymlinkedStoreFile(t *testing.T) {
	for _, name := range []string{"header.json", "store.json.gz"} {
		repo := t.TempDir()
		if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(repo, ".adamap", commitA, name)
		target := filepath.Join(t.TempDir(), "target")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(target, data, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
		symlinkOrSkip(t, target, path)
		if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "not a regular file") {
			t.Errorf("%s: Load err = %v, want a refusal saying it is not a regular file", name, err)
		}
	}
}

func TestLoadRefusesAnOversizedHeader(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".adamap", commitA, "header.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON, padded with whitespace to just over 1 MiB.
	padded := append(data, bytes.Repeat([]byte(" "), 1<<20+1-len(data))...)
	if err := os.WriteFile(path, padded, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "larger than") {
		t.Fatalf("Load err = %v, want one saying header.json is larger than the limit", err)
	}
}
