package rank

import (
	"cmp"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	// maxDetail is the most bytes a symbol's detail takes before it is clipped.
	maxDetail = 120
	// maxLabel is the most bytes a kind's label takes.
	maxLabel = 20
)

// Tokens estimates the tokens text takes: its bytes divided by 4, rounded up.
func Tokens(text string) int { return tokens(len(text)) }

func tokens(bytes int) int { return (bytes + 3) / 4 }

// Fit chooses what of files fits in budget tokens of Text. It takes symbols across all files by
// score, highest first (ties by path, then the file's own order), each with its file's path line
// the first time, and stops at the first that would take the text over the budget. The result
// keeps the files' order and each file's symbol order, and leaves out files with nothing chosen.
func Fit(files []File, budget int) []File {
	type candidate struct {
		file, sym int
	}
	var cands []candidate
	for f := range files {
		for s := range files[f].Symbols {
			cands = append(cands, candidate{f, s})
		}
	}
	slices.SortFunc(cands, func(a, b candidate) int {
		return cmp.Or(
			cmp.Compare(files[b.file].Symbols[b.sym].Score, files[a.file].Symbols[a.sym].Score),
			strings.Compare(files[a.file].Path, files[b.file].Path),
			cmp.Compare(a.file, b.file),
			cmp.Compare(a.sym, b.sym))
	})
	chosen := make([][]bool, len(files))
	used := 0
	for _, c := range cands {
		cost := len(symbolLine(files[c.file].Symbols[c.sym]))
		if chosen[c.file] == nil {
			cost += len(fileLine(files[c.file].Path))
		}
		if tokens(used+cost) > budget {
			break
		}
		used += cost
		if chosen[c.file] == nil {
			chosen[c.file] = make([]bool, len(files[c.file].Symbols))
		}
		chosen[c.file][c.sym] = true
	}
	var out []File
	for f, ch := range chosen {
		if ch == nil {
			continue
		}
		file := files[f]
		file.Symbols = nil
		for s, ok := range ch {
			if ok {
				file.Symbols = append(file.Symbols, files[f].Symbols[s])
			}
		}
		out = append(out, file)
	}
	return out
}

// Text is the map as plain text: each file's path on a line of its own, then a line for each of
// its symbols, indented, with its label and detail. A file without symbols is left out.
func Text(files []File) string {
	var b strings.Builder
	for _, f := range files {
		if len(f.Symbols) == 0 {
			continue
		}
		b.WriteString(fileLine(f.Path))
		for _, s := range f.Symbols {
			b.WriteString(symbolLine(s))
		}
	}
	return b.String()
}

// fileLine is a path's line, quoted when the path holds a control character or is not UTF-8, so
// one line stays one path.
func fileLine(path string) string {
	if !utf8.ValidString(path) || strings.ContainsFunc(path, unicode.IsControl) {
		path = strconv.Quote(path)
	}
	return path + "\n"
}

func symbolLine(s Symbol) string { return "  " + Label(s) + "  " + Detail(s) + "\n" }

// labels are the short labels of the SCIP kinds that have one; other kinds are shown in lower case.
var labels = map[string]string{
	"Function":       "func",
	"Method":         "method",
	"StaticMethod":   "method",
	"AbstractMethod": "method",
	"Variable":       "var",
	"Constant":       "const",
	"Type":           "type",
	"TypeAlias":      "type",
}

// Label is s's kind in a word: func, method, struct, type and so on. Without a kind it is read off
// the ID: func for a method descriptor, type for a type, macro for a macro, sym otherwise.
func Label(s Symbol) string {
	if s.Kind != "" {
		if l, ok := labels[s.Kind]; ok {
			return l
		}
		return clip(oneLine(strings.ToLower(s.Kind)), maxLabel)
	}
	switch {
	case strings.HasSuffix(s.ID, ")."):
		return "func"
	case strings.HasSuffix(s.ID, "#"):
		return "type"
	case strings.HasSuffix(s.ID, "!"):
		return "macro"
	}
	return "sym"
}

// Detail is what the map shows of s after its label, on one line of at most 120 bytes: the
// signature from the name on (leading keywords such as func, pub fn or type dropped), else the
// name and the first line of the doc, else the name.
func Detail(s Symbol) string {
	name := oneLine(s.Name)
	if sig := oneLine(s.Signature); sig != "" {
		// Only the start of a signature is shown; cutting it first bounds the work of dropKeywords.
		sig = dropKeywords(clipBytes(sig, 4*maxDetail), name)
		if name != "" && !hasWord(sig, name) {
			sig = name + " " + sig
		}
		return clip(sig, maxDetail)
	}
	if doc := oneLine(s.Doc); doc != "" {
		return clip(name+"  // "+doc, maxDetail)
	}
	return clip(name, maxDetail)
}

// oneLine turns runs of white space and control characters into one space, and invalid UTF-8
// into replacement characters, and trims the ends.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, strings.ToValidUTF8(s, "�"))
	return strings.Join(strings.Fields(s), " ")
}

// dropKeywords drops the words in lower-case letters at the start of sig (func, pub fn, type) while
// the name is still in what remains, and stops at the name itself.
func dropKeywords(sig, name string) string {
	for {
		i := strings.IndexByte(sig, ' ')
		if i <= 0 {
			return sig
		}
		w, rest := sig[:i], sig[i+1:]
		if w == name || strings.ContainsFunc(w, func(r rune) bool { return r < 'a' || r > 'z' }) || !hasWord(rest, name) {
			return sig
		}
		sig = rest
	}
}

// hasWord reports whether name appears in s as a whole word, not inside a longer identifier.
func hasWord(s, name string) bool {
	if name == "" {
		return false
	}
	for i := 0; ; {
		j := strings.Index(s[i:], name)
		if j < 0 {
			return false
		}
		start, end := i+j, i+j+len(name)
		before, _ := utf8.DecodeLastRuneInString(s[:start])
		after, _ := utf8.DecodeRuneInString(s[end:])
		if (start == 0 || !isIdent(before)) && (end == len(s) || !isIdent(after)) {
			return true
		}
		i = start + 1
	}
}

func isIdent(r rune) bool { return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r) }

// clipBytes cuts s to at most n bytes, never inside a character.
func clipBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// clip cuts s to at most n bytes, ending in "..." when it cuts, and never inside a character.
func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	i := n - len("...")
	for i > 0 && !utf8.RuneStart(s[i]) {
		i--
	}
	return s[:i] + "..."
}
