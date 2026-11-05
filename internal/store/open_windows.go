package store

import "os"

func openStoreFile(path string) (*os.File, error) {
	return os.Open(path)
}
