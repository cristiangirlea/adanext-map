package scip

import (
	"testing"

	scippb "github.com/scip-code/scip/bindings/go/scip"
	"google.golang.org/protobuf/proto"

	"github.com/cristiangirlea/adanext-map/internal/model"
	"github.com/cristiangirlea/adanext-map/internal/scip/sciptest"
)

// FuzzConvert feeds arbitrary bytes through proto.Unmarshal and the converter: whatever they hold,
// the converter returns a model or an error, and never panics.
func FuzzConvert(f *testing.F) {
	f.Add(sciptest.Bytes(f, sciptest.Index()))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		var x scippb.Index
		if err := proto.Unmarshal(data, &x); err != nil {
			return
		}
		idx, _, err := Convert(&x)
		if err == nil && idx == nil {
			t.Fatal("no model and no error")
		}
		if err != nil {
			return
		}
		for _, d := range idx.Documents {
			if perr := model.CheckPath(d.Path); perr != nil || d.Path != normalisePath(d.Path) {
				t.Fatalf("document path %q: %v", d.Path, perr)
			}
		}
	})
}
