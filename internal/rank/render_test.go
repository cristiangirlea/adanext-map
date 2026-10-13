package rank

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestTokensIsBytesOverFourRoundedUp(t *testing.T) {
	for text, want := range map[string]int{"": 0, "a": 1, "abcd": 1, "abcde": 2, "héllo": 2} {
		if got := Tokens(text); got != want {
			t.Errorf("Tokens(%q) = %d, want %d", text, got, want)
		}
	}
}

func TestLabelShortensTheKind(t *testing.T) {
	for _, c := range []struct {
		kind, id, want string
	}{
		{"Function", id("F"), "func"},
		{"Method", "m `p`/T#M().", "method"},
		{"Struct", "m `p`/T#", "struct"},
		{"Interface", "m `p`/I#", "interface"},
		{"TypeAlias", "m `p`/A#", "type"},
		{"Constant", "m `p`/C.", "const"},
		{"Variable", "m `p`/V.", "var"},
		{"", "m `p`/F().", "func"},
		{"", "m `p`/T#", "type"},
		{"", "m `p`/m!", "macro"},
		{"", "m `p`/T#f.", "sym"},
		{"Weird\nKind With A Name Far Too Long To Show", "m `p`/x.", "weird kind with a..."},
	} {
		if got := Label(Symbol{Kind: c.kind, ID: c.id}); got != c.want {
			t.Errorf("Label(%q, %q) = %q, want %q", c.kind, c.id, got, c.want)
		}
	}
}

func TestDetailShowsTheSignatureOrTheDocOnOneLine(t *testing.T) {
	for _, c := range []struct {
		name string
		s    Symbol
		want string
	}{
		{"go func", Symbol{Name: "Add", Signature: "func Add(a, b int) int"}, "Add(a, b int) int"},
		{"go method keeps its receiver", Symbol{Name: "Run", Signature: "func (r *Runner) Run() error"}, "(r *Runner) Run() error"},
		{"rust keywords", Symbol{Name: "run", Signature: "pub async fn run(a: u32) -> u32"}, "run(a: u32) -> u32"},
		{"rust visibility with a path", Symbol{Name: "x", Signature: "pub(crate) fn x()"}, "pub(crate) fn x()"},
		{"multi-line", Symbol{Name: "Server", Signature: "type Server struct {\n\tAddr string\n\tPort int\n}"}, "Server struct { Addr string Port int }"},
		{"signature without the name", Symbol{Name: "T", Signature: "struct{}"}, "T struct{}"},
		{"doc when no signature", Symbol{Name: "Hub", Doc: "Hub joins\tthe   spokes."}, "Hub  // Hub joins the spokes."},
		{"signature over doc", Symbol{Name: "F", Signature: "func F()", Doc: "F does it."}, "F()"},
		{"name only", Symbol{Name: "Bare"}, "Bare"},
		{"control characters", Symbol{Name: "a\x1b[2Jb"}, "a [2Jb"},
	} {
		if got := Detail(c.s); got != c.want {
			t.Errorf("%s: Detail = %q, want %q", c.name, got, c.want)
		}
	}
	long := Detail(Symbol{Name: "F", Signature: "func F(" + strings.Repeat("a int, ", 40) + ")"})
	if len(long) != maxDetail || !strings.HasSuffix(long, "...") {
		t.Errorf("a long signature: %d bytes %q, want %d ending in ...", len(long), long, maxDetail)
	}
	// Clipping never splits a character.
	wide := Detail(Symbol{Name: "é", Signature: "é " + strings.Repeat("é", 100)})
	if !strings.HasSuffix(wide, "...") || strings.ContainsRune(wide, '�') {
		t.Errorf("a wide signature clipped to %q", wide)
	}
}

func TestTextIsPathsThenIndentedSymbols(t *testing.T) {
	files := []File{
		{Path: "cmd/run.go", Symbols: []Symbol{
			{Name: "Run", Kind: "Function", Signature: "func Run(args []string, stdout, stderr io.Writer) int"},
			{Name: "Server", Kind: "Struct"},
		}},
		{Path: "empty.go"},
		{Path: "odd\npath.go", Symbols: []Symbol{{Name: "X", Kind: "Variable"}}},
	}
	want := `cmd/run.go
  func  Run(args []string, stdout, stderr io.Writer) int
  struct  Server
"odd\npath.go"
  var  X
`
	if got := Text(files); got != want {
		t.Errorf("Text:\n%s\nwant:\n%s", got, want)
	}
	if got := Text(nil); got != "" {
		t.Errorf("Text(nil) = %q", got)
	}
}

func TestTextQuotesAPathThatLooksLikeASymbolLine(t *testing.T) {
	// A symbol line starts with two spaces: a path that does would read as one under the wrong file.
	files := []File{{Path: "  func  Fake\n.go", Symbols: []Symbol{{Name: "X", Kind: "Variable"}}},
		{Path: "  fake.go", Symbols: []Symbol{{Name: "Y", Kind: "Variable"}}},
		{Path: " one.go", Symbols: []Symbol{{Name: "Z", Kind: "Variable"}}}}
	want := "\"  func  Fake\\n.go\"\n  var  X\n\"  fake.go\"\n  var  Y\n\" one.go\"\n  var  Z\n"
	if got := Text(files); got != want {
		t.Errorf("Text:\n%s\nwant:\n%s", got, want)
	}
}

func TestTextQuotesAPathThatIsNotUTF8(t *testing.T) {
	// A lone 0xff is no control or format character, only invalid UTF-8: it must not reach the terminal raw.
	files := []File{{Path: "a\xffb.go", Symbols: []Symbol{{Name: "X", Kind: "Variable"}}}}
	want := "\"a\\xffb.go\"\n  var  X\n"
	if got := Text(files); got != want {
		t.Errorf("Text %q, want %q", got, want)
	}
}

func TestTextQuotesAPathWithALineOrParagraphSeparator(t *testing.T) {
	// U+2028 and U+2029 break a line on some terminals and viewers, though they are not Cc or Cf.
	files := []File{
		{Path: "a\u2028b.go", Symbols: []Symbol{{Name: "X", Kind: "Variable"}}},
		{Path: "c\u2029d.go", Symbols: []Symbol{{Name: "Y\u2028Z", Kind: "Variable"}}},
	}
	want := "\"a\\u2028b.go\"\n  var  X\n\"c\\u2029d.go\"\n  var  Y Z\n"
	if got := Text(files); got != want {
		t.Errorf("Text %q, want %q", got, want)
	}
}

func TestFitNeverExceedsTheBudget(t *testing.T) {
	files := Rank(synthetic(120, 8, 30), nil).Files
	all, prev := Text(files), 0
	for budget := 50; budget <= 5000; budget++ {
		text := Text(Fit(files, budget))
		got := Tokens(text)
		if got > budget {
			t.Fatalf("budget %d: the map takes %d tokens", budget, got)
		}
		// A larger budget only adds to the map.
		if got < prev {
			t.Fatalf("budget %d: the map takes %d tokens, less than the %d of a smaller budget", budget, got, prev)
		}
		prev = got
		// Short of the whole map, it stops only when the next line would not fit, and no line
		// here takes more than 50 tokens.
		if text != all && got <= budget-50 {
			t.Fatalf("budget %d: the map takes only %d tokens", budget, got)
		}
	}
}

func TestFitTakesAnExactFit(t *testing.T) {
	// "a.go\n" and "  func  Abcde\n" are 5 + 14 bytes, "abc.go\n" and "  func  X\n" 7 + 10: 36 bytes
	// in all, exactly 9 tokens.
	files := []File{
		{Path: "a.go", Rank: 0.6, Symbols: []Symbol{{Name: "Abcde", Kind: "Function", Score: 0.6}}},
		{Path: "abc.go", Rank: 0.4, Symbols: []Symbol{{Name: "X", Kind: "Function", Score: 0.4}}},
	}
	text := Text(files)
	if len(text)%4 != 0 {
		t.Fatalf("the fixture is %d bytes, not a whole number of tokens: %q", len(text), text)
	}
	exact := len(text) / 4
	if got := Text(Fit(files, exact)); got != text {
		t.Errorf("budget %d (exact): %q, want all of %q", exact, got, text)
	}
	if got := Text(Fit(files, exact-1)); got != "a.go\n  func  Abcde\n" {
		t.Errorf("budget %d: %q, want only the first file", exact-1, got)
	}
	if got := Fit(files, 0); got != nil {
		t.Errorf("budget 0: %v, want nothing", got)
	}
}

func TestFitChoosesBySymbolScoreAndKeepsFileOrder(t *testing.T) {
	files := []File{
		{Path: "big.go", Rank: 0.6, Symbols: []Symbol{
			{Name: "B1", Kind: "Function", Score: 0.15},
			{Name: "B2", Kind: "Function", Score: 0.15},
			{Name: "B3", Kind: "Function", Score: 0.15},
			{Name: "B4", Kind: "Function", Score: 0.15},
		}},
		{Path: "one.go", Rank: 0.4, Symbols: []Symbol{{Name: "O1", Kind: "Function", Score: 0.4}}},
	}
	// Room for two files and two symbols: one.go's single symbol outscores each of big.go's.
	got := Fit(files, Tokens("big.go\n  func  B1\none.go\n  func  O1\n"))
	if len(got) != 2 || got[0].Path != "big.go" || got[1].Path != "one.go" {
		t.Fatalf("files %v, want big.go then one.go", got)
	}
	if n := names(got[0]); !slices.Equal(n, []string{"B1"}) {
		t.Errorf("big.go symbols %v, want [B1]", n)
	}
	// Room for one file and one symbol: one.go, though it ranks second.
	got = Fit(files, Tokens("one.go\n  func  O1\n"))
	if len(got) != 1 || got[0].Path != "one.go" {
		t.Errorf("files %v, want one.go alone", got)
	}
}

func TestFitStopsAtTheFirstSymbolThatDoesNotFit(t *testing.T) {
	long := strings.Repeat("L", 40)
	files := []File{{Path: "a.go", Symbols: []Symbol{
		{Name: "A", Kind: "Variable", Score: 0.5},
		{Name: long, Kind: "Variable", Score: 0.3},
		{Name: "C", Kind: "Variable", Score: 0.1},
	}}}
	// "a.go\n  var  A\n" is 14 bytes; the long symbol's line takes 49 more, so it misses a budget of
	// 8 tokens (32 bytes). C's line is 9 bytes and would still fit, but it scores lower than the
	// symbol that missed: filling stops there, as docs/map.md says, and C is not taken.
	if got := Text(Fit(files, 8)); got != "a.go\n  var  A\n" {
		t.Errorf("budget 8: %q, want only A", got)
	}
}

func TestFitIsDeterministic(t *testing.T) {
	idx := synthetic(300, 8, 40)
	a := Text(Fit(Rank(idx, nil).Files, 2000))
	b := Text(Fit(Rank(idx, nil).Files, 2000))
	if a != b || a == "" {
		t.Fatalf("two maps differ, or are empty:\n%s\n---\n%s", a, b)
	}
}

func TestFormatCharactersInANameDocOrKindBecomeASpace(t *testing.T) {
	// U+202E reverses what follows it on a terminal, U+200B takes no room, U+2028 breaks the line.
	files := []File{{Path: "a.go", Symbols: []Symbol{
		{Name: "Ru\u202en", Kind: "Function"},
		{Name: "Hub", Doc: "joins\u200bthe\u2028spokes"},
		{Name: "K", Kind: "Fancy\u202eKind"},
	}}}
	text := Text(files)
	if strings.ContainsAny(text, "\u202e\u200b\u2028") {
		t.Errorf("format characters reach the text: %q", text)
	}
	want := "a.go\n  func  Ru n\n  sym  Hub  // joins the spokes\n  fancy kind  K\n"
	if text != want {
		t.Errorf("text %q, want %q", text, want)
	}
}

func TestDetailOfAHugeSignatureIsFast(t *testing.T) {
	// Every leading word is a keyword candidate, and each check scans what follows it.
	sig := strings.Repeat("a ", 512*1024) + "Name"
	start := time.Now()
	got := Detail(Symbol{Name: "Name", Signature: sig})
	limit := time.Second
	if raceEnabled {
		limit *= 10
	}
	if d := time.Since(start); d > limit {
		t.Errorf("Detail took %v on a %d byte signature, want under %v", d, len(sig), limit)
	}
	if len(got) > maxDetail || !strings.HasSuffix(got, "...") {
		t.Errorf("Detail = %q, want at most %d bytes ending in ...", got, maxDetail)
	}
}

func TestTextNeutralisesFormatCharacters(t *testing.T) {
	// U+202E reverses what follows it on a terminal, U+200B takes no room: neither may reach the output.
	files := []File{{Path: "a\u202Eog.sh\u200B", Symbols: []Symbol{
		{Name: "Run", Kind: "Function", Signature: "func Run(x\u202Ey int)"},
	}}}
	text := Text(files)
	if strings.ContainsAny(text, "\u202E\u200B") {
		t.Errorf("format characters reach the text: %q", text)
	}
	want := "\"a\\u202eog.sh\\u200b\"\n  func  Run(x y int)\n"
	if text != want {
		t.Errorf("text %q, want %q", text, want)
	}
}
