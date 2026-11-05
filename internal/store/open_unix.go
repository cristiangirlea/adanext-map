//go:build unix

package store

import (
	"os"
	"syscall"
)

func openStoreFile(path string) (*os.File, error) {
	// A FIFO swapped in after Lstat must not block before the descriptor check.
	return os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
}
