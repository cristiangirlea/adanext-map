//go:build race

package store

// checkAllocations is whether the encoder test bounds what encoding allocates. The race detector
// allocates for its own bookkeeping, so a run with -race checks only the size of each write.
const checkAllocations = false
