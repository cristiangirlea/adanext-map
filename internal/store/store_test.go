package store

import (
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
