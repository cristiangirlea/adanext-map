package mcpserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStampEqualWithoutFileIdentity(t *testing.T) {
	path := filepath.Join(t.TempDir(), "latest")
	if err := os.WriteFile(path, []byte("commit"), 0600); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	a := stamp{mod: time.Unix(100, 0), size: 40, header: time.Unix(200, 0)}
	for _, tt := range []struct {
		name        string
		left, right os.FileInfo
	}{
		{"both unavailable", nil, nil},
		{"left unavailable", nil, fi},
		{"right unavailable", fi, nil},
	} {
		t.Run(tt.name, func(t *testing.T) {
			left, right := a, a
			left.file, right.file = tt.left, tt.right
			if !left.equal(right) {
				t.Error("matching metadata should match when file identity is unavailable")
			}
			for _, field := range []string{"mod", "size", "header"} {
				t.Run(field, func(t *testing.T) {
					changed := right
					switch field {
					case "mod":
						changed.mod = changed.mod.Add(time.Second)
					case "size":
						changed.size++
					case "header":
						changed.header = changed.header.Add(time.Second)
					}
					if left.equal(changed) {
						t.Error("changed metadata should not match")
					}
				})
			}
		})
	}
}
