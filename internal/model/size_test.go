package model

import (
	"strconv"
	"testing"
	"unsafe"
)

func TestStructSizesMatchDocumentedMemoryEstimate(t *testing.T) {
	if strconv.IntSize != 64 {
		t.Skip("docs/index.md estimates struct sizes for 64-bit Go")
	}
	// Keep docs/index.md's memory estimate in step with the model's layout.
	for _, tc := range []struct {
		name string
		got  uintptr
		want uintptr
	}{
		{"Document", unsafe.Sizeof(Document{}), 80},
		{"Symbol", unsafe.Sizeof(Symbol{}), 112},
		{"Occurrence", unsafe.Sizeof(Occurrence{}), 64},
		{"Relationship", unsafe.Sizeof(Relationship{}), 24},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.got != tc.want {
				t.Errorf("unsafe.Sizeof(%s{}) = %d, want %d; update docs/index.md's memory estimate when changing the layout", tc.name, tc.got, tc.want)
			}
		})
	}
}
