//go:build !windows

package mcpserver

// isSharingViolation is Windows' error for opening a file at the instant it is renamed over; no
// other system has one.
func isSharingViolation(error) bool { return false }
