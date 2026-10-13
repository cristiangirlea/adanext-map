package store

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/cristiangirlea/adanext-map/internal/model"
)

// decodeIndex reads the store's JSON from r one element at a time, counting documents, symbols,
// occurrences and relationships as they arrive, and stops with an error naming the limit the moment
// one is passed. json.Decoder.Decode would read a whole array first, and each tiny `{},` in it
// becomes a struct: the bytes read would not bound the memory. The result is what Decode gives for
// the same document (the keys match exactly, where encoding/json also ignores case).
func decodeIndex(r io.Reader, lim model.Limits) (*model.Index, error) {
	d := &decoder{dec: json.NewDecoder(r), lim: lim}
	var idx model.Index
	err := d.object(func(key string) (err error) {
		switch key {
		case "tool":
			return d.dec.Decode(&idx.Tool)
		case "project_root":
			return d.dec.Decode(&idx.ProjectRoot)
		case "documents":
			idx.Documents, err = readArray(d, func(doc *model.Document) error {
				if d.docs++; d.docs > lim.Documents {
					return model.ErrOverLimit("documents", lim.Documents)
				}
				return d.document(doc)
			})
		case "symbols":
			idx.Symbols, err = readArray(d, d.symbol)
		default:
			err = d.skip()
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return &idx, nil
}

// decoder carries the counts so far. The symbol count is global and local symbols together, as
// model.Counts has it.
type decoder struct {
	dec                    *json.Decoder
	lim                    model.Limits
	docs, syms, occs, rels int
}

func (d *decoder) document(doc *model.Document) error {
	return d.object(func(key string) (err error) {
		switch key {
		case "path":
			return d.dec.Decode(&doc.Path)
		case "language":
			return d.dec.Decode(&doc.Language)
		case "locals":
			doc.Locals, err = readArray(d, d.symbol)
		case "occurrences":
			doc.Occurrences, err = readArray(d, func(o *model.Occurrence) error {
				if d.occs++; d.occs > d.lim.Occurrences {
					return model.ErrOverLimit("occurrences", d.lim.Occurrences)
				}
				return d.dec.Decode(o)
			})
		default:
			err = d.skip()
		}
		return err
	})
}

func (d *decoder) symbol(s *model.Symbol) error {
	if d.syms++; d.syms > d.lim.Symbols {
		return model.ErrOverLimit("symbols", d.lim.Symbols)
	}
	return d.object(func(key string) (err error) {
		switch key {
		case "id":
			return d.dec.Decode(&s.ID)
		case "display_name":
			return d.dec.Decode(&s.DisplayName)
		case "kind":
			return d.dec.Decode(&s.Kind)
		case "doc":
			return d.dec.Decode(&s.Doc)
		case "signature":
			return d.dec.Decode(&s.Signature)
		case "definition":
			return d.dec.Decode(&s.Definition)
		case "relationships":
			s.Relationships, err = readArray(d, func(rel *model.Relationship) error {
				if d.rels++; d.rels > d.lim.Relationships {
					return model.ErrOverLimit("relationships", d.lim.Relationships)
				}
				return d.dec.Decode(rel)
			})
		default:
			err = d.skip()
		}
		return err
	})
}

// object walks the JSON object at the decoder's position, calling field with each key; field must
// consume the key's value. null is an empty object, as in encoding/json.
func (d *decoder) object(field func(key string) error) error {
	tok, err := d.dec.Token()
	if err != nil {
		return eof(err)
	}
	if tok == nil {
		return nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return fmt.Errorf("expected an object, found %v", tok)
	}
	for d.dec.More() {
		k, err := d.dec.Token()
		if err != nil {
			return eof(err)
		}
		if err := field(k.(string)); err != nil {
			return eof(err)
		}
	}
	_, err = d.dec.Token() // the closing brace, or the error that ended More early
	return eof(err)
}

// readArray reads the JSON array at the decoder's position one element at a time. null is a nil
// slice and [] an empty one, as in encoding/json.
func readArray[T any](d *decoder, elem func(*T) error) ([]T, error) {
	tok, err := d.dec.Token()
	if err != nil {
		return nil, eof(err)
	}
	if tok == nil {
		return nil, nil
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '[' {
		return nil, fmt.Errorf("expected an array, found %v", tok)
	}
	out := []T{}
	for d.dec.More() {
		var v T
		if err := elem(&v); err != nil {
			return nil, eof(err)
		}
		out = append(out, v)
	}
	_, err = d.dec.Token() // the closing bracket, or the error that ended More early
	return out, eof(err)
}

// skip consumes the value at the decoder's position, token by token, so a field this adamap does not
// know is never built in memory.
func (d *decoder) skip() error {
	depth := 0
	for {
		tok, err := d.dec.Token()
		if err != nil {
			return eof(err)
		}
		if delim, ok := tok.(json.Delim); ok {
			if delim == '[' || delim == '{' {
				depth++
			} else {
				depth--
			}
		}
		if depth == 0 {
			return nil
		}
	}
}

// eof turns a bare end of input into the error of a document cut short.
func eof(err error) error {
	if err == io.EOF {
		return io.ErrUnexpectedEOF
	}
	return err
}
