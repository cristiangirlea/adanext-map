package mcpserver

import (
	"errors"
	"syscall"
)

// errorSharingViolation is Win32's ERROR_SHARING_VIOLATION, which package syscall does not name.
const errorSharingViolation = syscall.Errno(32)

// isSharingViolation selects the Windows errors covered by the bounded latest-read retry.
func isSharingViolation(err error) bool {
	return errors.Is(err, errorSharingViolation) || errors.Is(err, syscall.ERROR_ACCESS_DENIED)
}
