package store

import (
	"bytes"
	"encoding/json"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// encodeIndex writes idx to w as encoding/json encodes it, except that <, > and & stay as they are
// (Rust symbols are full of them), one document and one symbol at a time: only the largest element
// is ever in memory, never the whole JSON. The fields come in model.Index's order and are left out
// under its omitempty tags, so any JSON reader, decodeIndex included, gets the same model.
func encodeIndex(w io.Writer, idx *model.Index) error {
	e := &encoder{w: w}
	e.enc = json.NewEncoder(&e.buf)
	e.enc.SetEscapeHTML(false)
	e.value(`{"tool":`, &idx.Tool)
	if idx.ProjectRoot != "" {
		e.value(`,"project_root":`, idx.ProjectRoot)
	}
	if len(idx.Documents) > 0 {
		writeArray(e, `,"documents":`, idx.Documents)
	}
	if len(idx.Symbols) > 0 {
		writeArray(e, `,"symbols":`, idx.Symbols)
	}
	e.raw("}\n")
	return e.err
}

// encoder keeps the first error, after which it writes nothing.
type encoder struct {
	w   io.Writer
	buf bytes.Buffer
	enc *json.Encoder
	err error
}

func (e *encoder) raw(s string) {
	if e.err == nil {
		_, e.err = io.WriteString(e.w, s)
	}
}

// value writes prefix and v's encoding in one write, without the newline json.Encoder ends it with.
func (e *encoder) value(prefix string, v any) {
	if e.err != nil {
		return
	}
	e.buf.Reset()
	e.buf.WriteString(prefix)
	if e.err = e.enc.Encode(v); e.err != nil {
		return
	}
	_, e.err = e.w.Write(bytes.TrimSuffix(e.buf.Bytes(), []byte("\n")))
}

// writeArray writes key and elems as a JSON array, one element at a time. Each element goes by
// pointer, so it is not copied into the interface.
func writeArray[T any](e *encoder, key string, elems []T) {
	for i := range elems {
		sep := ","
		if i == 0 {
			sep = key + "["
		}
		e.value(sep, &elems[i])
	}
	e.raw("]")
}
