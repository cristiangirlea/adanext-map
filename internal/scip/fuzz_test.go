package scip

import (
	"reflect"
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// FuzzConvert checks streamed decoding against conversion of the full protobuf index.
func FuzzConvert(f *testing.F) {
	f.Add(sciptest.Bytes(f, sciptest.Index()))
	f.Add([]byte{})
	f.Add(wireOrderIndex(f))
	f.Add(overlongDocumentLength(f))
	data := sciptest.Bytes(f, sciptest.Index())
	f.Add(append([]byte{data[0] | 0x80, 0}, data[1:]...))
	f.Fuzz(func(t *testing.T, data []byte) {
		streamed, skipped, streamErr := Decode(data)
		var x scippb.Index
		if err := proto.Unmarshal(data, &x); err != nil {
			if streamErr == nil {
				t.Fatal("streamed decoding accepted invalid protobuf")
			}
			return
		}
		idx, wantSkipped, err := Convert(&x)
		if err == nil && idx == nil {
			t.Fatal("no model and no error")
		}
		if err != nil {
			if streamErr == nil {
				t.Fatal("streamed decoding accepted an index that conversion refused")
			}
			return
		}
		if streamErr != nil {
			t.Fatal(streamErr)
		}
		if !reflect.DeepEqual(streamed, idx) || !reflect.DeepEqual(skipped, wantSkipped) {
			t.Fatal("streamed decoding differs from full conversion")
		}
		for _, d := range idx.Documents {
			if perr := model.CheckPath(d.Path); perr != nil || d.Path != normalisePath(d.Path) {
				t.Fatalf("document path %q: %v", d.Path, perr)
			}
		}
	})
}
