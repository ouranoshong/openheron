//go:build windows

package persist

import (
	"errors"

	"golang.org/x/sys/windows"
)

// isTransientRenameError reports whether the failure is "a handle still has
// the destination open", which clears on its own.
func isTransientRenameError(err error) bool {
	return errors.Is(err, windows.ERROR_ACCESS_DENIED) ||
		errors.Is(err, windows.ERROR_SHARING_VIOLATION) ||
		errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}
