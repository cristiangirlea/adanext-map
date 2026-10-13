package store

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
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
	idx, _, err := scip.Convert(sciptest.Index())
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
	if got, want := names(t, filepath.Join(repo, ".adamap")), []string{".gitignore", commitA, "latest"}; !slices.Equal(got, want) {
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
	if err == nil || !strings.Contains(err.Error(), "format 2") || !strings.Contains(err.Error(), `adamap "0.9.0" or later`) {
		t.Fatalf("err = %v, want one naming format 2 and adamap \"0.9.0\" or later", err)
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

func TestWriteKeepsTheStoreOutOfGit(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(filepath.Join(repo, ".adamap", ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "*\n" {
		t.Errorf(".adamap/.gitignore = %q, want %q", got, "*\n")
	}
	// The file does not get in the way of reading the store back.
	if _, _, err := Load(repo, commitA); err != nil {
		t.Errorf("Load: %v", err)
	}
	if c, err := Latest(repo); err != nil || c != commitA {
		t.Errorf("Latest = %q, %v, want %s", c, err, commitA)
	}
}

func TestWriteLeavesAnExistingGitignoreAlone(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".adamap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const mine = "# the user's own\n*.tmp\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != mine {
		t.Errorf(".adamap/.gitignore = %q, want it left as %q", got, mine)
	}
}

func TestWriteRefusesASymlinkedGitignore(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	dir := filepath.Join(repo, ".adamap")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(outside, "target")
	if err := os.WriteFile(target, []byte("untouched\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, target, filepath.Join(dir, ".gitignore"))
	if _, err := Write(repo, header(commitA), fixture(t)); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Write err = %v, want a refusal naming the symbolic link", err)
	}
	if got, _ := os.ReadFile(target); string(got) != "untouched\n" {
		t.Errorf("Write changed the link's target to %q", got)
	}
}

func TestWriteRefusesAGitignoreThatIsNotAFile(t *testing.T) {
	repo := t.TempDir()
	dir := filepath.Join(repo, ".adamap")
	if err := os.MkdirAll(filepath.Join(dir, ".gitignore"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := Write(repo, header(commitA), fixture(t))
	if err == nil || !strings.Contains(err.Error(), "is not a regular file") {
		t.Fatalf("Write err = %v, want a refusal saying it is not a regular file", err)
	}
	if !strings.Contains(err.Error(), filepath.Join(dir, ".gitignore")) {
		t.Errorf("Write err = %v, want it to name the .gitignore", err)
	}
}

func TestPublishNewKeepsAFileThatAppearedFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".gitignore")
	tmp := filepath.Join(dir, ".gitignore.1.tmp")
	if err := os.WriteFile(tmp, []byte("*\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Someone made the file after ensureGitignore looked and before it published.
	const theirs = "# theirs\n"
	if err := os.WriteFile(path, []byte(theirs), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := publishNew(tmp, path, []byte("*\n")); err != nil {
		t.Fatalf("publishNew over an existing file: %v", err)
	}
	if got, _ := os.ReadFile(path); string(got) != theirs {
		t.Errorf("publishNew replaced the file: %q, want %q", got, theirs)
	}
}

func TestGitSeesNothingUnderTheStore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not on PATH")
	}
	// No global or system ignore, not even git's default one under XDG_CONFIG_HOME, may hide the
	// stores: only the .gitignore Write made counts.
	home := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", os.DevNull)
	t.Setenv("GIT_CONFIG_SYSTEM", os.DevNull)
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", home)
	repo := t.TempDir()
	if out, err := exec.Command("git", "-C", repo, "init", "-q").CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("git", "-C", repo, "status", "--porcelain", "--untracked-files=all").CombinedOutput()
	if err != nil {
		t.Fatalf("git status: %v\n%s", err, out)
	}
	if strings.TrimSpace(string(out)) != "" {
		t.Errorf("git status lists:\n%s", out)
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

func TestLoadReadsAHeaderOfExactlyTheLimit(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(repo, ".adamap", commitA, "header.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// Valid JSON, padded with whitespace to exactly 1 MiB.
	padded := append(data, bytes.Repeat([]byte(" "), 1<<20-len(data))...)
	if len(padded) != maxHeaderBytes {
		t.Fatalf("test header is %d bytes, want %d", len(padded), maxHeaderBytes)
	}
	if err := os.WriteFile(path, padded, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := Load(repo, commitA); err != nil {
		t.Fatalf("Load of a header of exactly %d bytes: %v", maxHeaderBytes, err)
	}
}

func TestLoadRefusesASymbolDefinitionPathOutsideTheProject(t *testing.T) {
	loc := func(p string) *model.Location { return &model.Location{Path: p} }
	cases := map[string]func(idx *model.Index){
		"symbol 0": func(idx *model.Index) {
			idx.Symbols = []model.Symbol{{ID: "s", Definition: loc("../../etc/passwd")}}
		},
		"document 0 local 0": func(idx *model.Index) {
			idx.Documents[0].Locals = []model.Symbol{{ID: "local 0", Definition: loc("/etc/passwd")}}
		},
	}
	for want, change := range cases {
		repo := t.TempDir()
		idx := fixture(t)
		change(idx)
		if _, err := Write(repo, header(commitA), idx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("Load err = %v, want one naming %s", err, want)
		}
	}
}

func TestLatestRefusesASymlinkedLatest(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".adamap"), 0o755); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(secret, []byte(commitA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, secret, filepath.Join(repo, ".adamap", "latest"))
	if c, err := Latest(repo); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("Latest = %q, %v; want a refusal saying it is not a regular file", c, err)
	}
}

func TestLatestRefusesASymlinkedStoreDirectory(t *testing.T) {
	repo, outside := t.TempDir(), t.TempDir()
	if err := os.WriteFile(filepath.Join(outside, "latest"), []byte(commitA+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	symlinkOrSkip(t, outside, filepath.Join(repo, ".adamap"))
	if c, err := Latest(repo); err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("Latest = %q, %v; want a refusal naming the symbolic link", c, err)
	}
}

func TestLatestRefusesACommitFollowedByMoreThanTheLimit(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".adamap"), 0o755); err != nil {
		t.Fatal(err)
	}
	content := commitA + strings.Repeat(" ", 200) + "x"
	if err := os.WriteFile(filepath.Join(repo, ".adamap", "latest"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if c, err := Latest(repo); err == nil {
		t.Fatalf("Latest = %q, want an error for a file longer than the limit", c)
	}
}

// useOpenFile makes the store read its files through open for the rest of the test.
func useOpenFile(t *testing.T, open func(string) (*os.File, error)) {
	t.Helper()
	was := openFile
	openFile = open
	t.Cleanup(func() { openFile = was })
}

func TestLatestReportsAnOpenFailureAsItIs(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	useOpenFile(t, func(path string) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrPermission}
	})
	_, err := Latest(repo)
	if !errors.Is(err, fs.ErrPermission) {
		t.Fatalf("Latest err = %v, want the permission error", err)
	}
	if strings.Contains(err.Error(), "no store") {
		t.Errorf("Latest err = %v, want no claim that there is no store", err)
	}
}

func TestLatestSaysNoStoreWhenTheFileIsGoneAtOpen(t *testing.T) {
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	useOpenFile(t, func(path string) (*os.File, error) {
		return nil, &fs.PathError{Op: "open", Path: path, Err: fs.ErrNotExist}
	})
	if _, err := Latest(repo); err == nil || !strings.Contains(err.Error(), "no store in") {
		t.Fatalf("Latest err = %v, want one saying there is no store yet", err)
	}
}

func TestStoreFilesAreRefusedWhenTheOpenedFileIsNotTheOneChecked(t *testing.T) {
	// A file swapped in between the Lstat and the open (a link to a device, say) must not be read.
	// The stand-in is a copy with the same bytes, so only the identity check can tell.
	for _, name := range []string{headerFile, storeFile, latestFile} {
		t.Run(name, func(t *testing.T) {
			repo := t.TempDir()
			if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
				t.Fatal(err)
			}
			useOpenFile(t, func(path string) (*os.File, error) {
				if filepath.Base(path) != name {
					return os.Open(path)
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return nil, err
				}
				other := filepath.Join(t.TempDir(), name)
				if err := os.WriteFile(other, data, 0o644); err != nil {
					return nil, err
				}
				return os.Open(other)
			})
			_, _, loadErr := Load(repo, commitA)
			_, latestErr := Latest(repo)
			err := loadErr
			if name == latestFile {
				err = latestErr
			}
			if err == nil || !strings.Contains(err.Error(), "is not the file that was checked") {
				t.Fatalf("err = %v, want a refusal saying the opened file is not the one checked", err)
			}
		})
	}
}

func TestLatestErrorDoesNotQuoteTheWholeFile(t *testing.T) {
	repo := t.TempDir()
	if err := os.MkdirAll(filepath.Join(repo, ".adamap"), 0o755); err != nil {
		t.Fatal(err)
	}
	tail := "SECRET-TAIL-MARKER"
	content := strings.Repeat("x", 1000) + tail
	if err := os.WriteFile(filepath.Join(repo, ".adamap", "latest"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Latest(repo)
	if err == nil {
		t.Fatal("Latest accepted garbage")
	}
	if strings.Contains(err.Error(), tail) || strings.Contains(err.Error(), strings.Repeat("x", 41)) {
		t.Errorf("err quotes more than the first 40 bytes: %v", err)
	}
	if !strings.Contains(err.Error(), "not a commit") {
		t.Errorf("err = %v, want one saying it is not a commit", err)
	}
}

func TestLoadRefusesADocumentPathWindowsCannotHold(t *testing.T) {
	for _, p := range []string{"a.go:x", "pkg/a:x.go", "a\x00b.go", "a\nb.go",
		"nul", "pkg/con.txt", "AUX.go", "com1.", "lpt³"} {
		repo := t.TempDir()
		idx := fixture(t)
		idx.Documents[0].Path = p // as a hand-edited store would have it; Convert would skip it
		if _, err := Write(repo, header(commitA), idx); err != nil {
			t.Fatal(err)
		}
		if _, _, err := Load(repo, commitA); err == nil || !strings.Contains(err.Error(), "document 0") {
			t.Errorf("path %q: Load err = %v, want one naming document 0", p, err)
		}
	}
}

// A store is untrusted too (a cloned repository can commit one): text from its header must reach
// the terminal quoted, never raw.
func TestLoadErrorQuotesTheAdamapVersionOfANewerStore(t *testing.T) {
	const hostile = "9\x1b[31m\u202e.0"
	repo := t.TempDir()
	if _, err := Write(repo, header(commitA), fixture(t)); err != nil {
		t.Fatal(err)
	}
	rewriteHeader(t, repo, func(h map[string]any) { h["format"] = 2; h["adamap"] = hostile })
	_, _, err := Load(repo, commitA)
	if err == nil {
		t.Fatal("no error")
	}
	if strings.ContainsAny(err.Error(), "\x1b\u202e") {
		t.Fatalf("err holds a raw escape or override: %q", err.Error())
	}
	if !strings.Contains(err.Error(), strconv.Quote(hostile)) {
		t.Fatalf("err = %q, want the version quoted as %s", err, strconv.Quote(hostile))
	}
}
