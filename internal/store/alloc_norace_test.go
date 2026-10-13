//go:build !race

package store

// checkAllocations is whether the encoder test bounds what encoding allocates; see alloc_race_test.go.
const checkAllocations = true
