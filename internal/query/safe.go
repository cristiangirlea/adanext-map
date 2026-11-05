package query

import (
	"fmt"
	"strconv"
	"unicode/utf8"
)

// Safe is s as a text printer should write it. The names, kinds, symbol strings and paths of a
// store come from an indexer's output and may be hostile, and a terminal acts on a raw ESC
// (clipboard writes, title changes, overwriting lines) and on bidirectional overrides and
// zero-width characters (hiding what a line says). Safe returns s unchanged when it is valid
// UTF-8 and every rune is printable, and strconv.Quote of it otherwise. Printable leaves out the
// format characters (Unicode category Cf) that bidi overrides and zero-width characters belong to.
// JSON output does not use it: encoding/json escapes what it must.
func Safe(s string) string {
	if utf8.ValidString(s) {
		ok := true
		for _, r := range s {
			if !strconv.IsPrint(r) {
				ok = false
				break
			}
		}
		if ok {
			return s
		}
	}
	return strconv.Quote(s)
}

// EscapeJSONControls replaces raw DEL and C1 controls in encoded JSON with Unicode escapes.
func EscapeJSONControls(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); i++ {
		switch {
		case in[i] == 0x7f:
			out = append(out, `\u007f`...)
		case in[i] == 0xc2 && i+1 < len(in) && in[i+1] >= 0x80 && in[i+1] <= 0x9f:
			out = fmt.Appendf(out, `\u%04x`, in[i+1])
			i++
		default:
			out = append(out, in[i])
		}
	}
	return out
}
