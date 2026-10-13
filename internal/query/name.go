package query

import "strings"

// nameFromSymbol reads the name of the last descriptor out of a global SCIP symbol string, for a
// symbol the indexer never described and so gave no display name. It returns "" for anything it
// cannot read, a local symbol included.
//
// A symbol is `<scheme> <manager> <package> <version> <descriptors>`, a space inside one of the
// first four written as two. A descriptor is a name and a suffix: `/` namespace, `#` type, `.`
// term, `:` meta, `!` macro, `(disambiguator).` method; `[name]` is a type parameter and `(name)` a
// parameter. A name is plain, or in backticks with a literal backtick doubled.
func nameFromSymbol(id string) string {
	i := 0
	for range 4 {
		var ok bool
		if i, ok = skipField(id, i); !ok {
			return ""
		}
	}
	d := id[i:]
	var name string
	for i := 0; i < len(d); {
		switch d[i] {
		case '[', '(':
			// A type parameter or a parameter: the name sits between the brackets.
			closing := byte(']')
			if d[i] == '(' {
				closing = ')'
			}
			end := strings.IndexByte(d[i+1:], closing)
			if end < 0 {
				return ""
			}
			n, next, ok := readName(d[:i+1+end], i+1)
			if !ok || next != i+1+end {
				return ""
			}
			name, i = n, i+end+2
		default:
			n, next, ok := readName(d, i)
			if !ok || next >= len(d) {
				return ""
			}
			switch d[next] {
			case '/', '#', '.', ':', '!':
				name, i = n, next+1
			case '(':
				end := strings.IndexByte(d[next:], ')')
				if end < 0 || next+end+1 >= len(d) || d[next+end+1] != '.' {
					return ""
				}
				name, i = n, next+end+2
			default:
				return ""
			}
		}
	}
	return name
}

// skipField returns the index after the field that starts at i and its separating space, treating
// two spaces as a space inside the field.
func skipField(s string, i int) (int, bool) {
	for i < len(s) {
		if s[i] != ' ' {
			i++
		} else if i+1 < len(s) && s[i+1] == ' ' {
			i += 2
		} else {
			return i + 1, true
		}
	}
	return i, false
}

// readName reads a descriptor name that starts at s[i]: the name, the index after it, and whether
// there was one.
func readName(s string, i int) (string, int, bool) {
	if i >= len(s) {
		return "", i, false
	}
	if s[i] == '`' {
		var b strings.Builder
		for j := i + 1; j < len(s); j++ {
			switch {
			case s[j] != '`':
				b.WriteByte(s[j])
			case j+1 < len(s) && s[j+1] == '`':
				b.WriteByte('`')
				j++
			default:
				return b.String(), j + 1, b.Len() > 0
			}
		}
		return "", len(s), false
	}
	j := i
	for j < len(s) && isSimple(s[j]) {
		j++
	}
	return s[i:j], j, j > i
}

// isSimple reports whether c may appear in a name that is not in backticks.
func isSimple(c byte) bool {
	return c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' ||
		c == '_' || c == '+' || c == '-' || c == '$'
}
