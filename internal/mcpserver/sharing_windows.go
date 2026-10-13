package mcpserver

import (
	"errors"
	"syscall"
)

// errorSharingViolation is Win32's ERROR_SHARING_VIOLATION, which package syscall does not name.
const errorSharingViolation = syscall.Errno(32)

// isSharingViolation says whether err is Windows refusing to open a file another process has open
// without sharing, as happens at the instant a file is renamed over.
func isSharingViolation(err error) bool { return errors.Is(err, errorSharingViolation) }
