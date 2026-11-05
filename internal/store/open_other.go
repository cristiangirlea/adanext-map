//go:build !unix && !windows

package store

import "os"

func openStoreFile(path string) (*os.File, error) {
	// Non-Unix systems cannot rely on the Unix nonblocking open flag.
	return os.Open(path)
}
