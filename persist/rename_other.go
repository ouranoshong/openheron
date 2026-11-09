//go:build !windows

package persist

// isTransientRenameError is always false elsewhere: rename over an open file
// is allowed, so a failure means something the caller must see.
func isTransientRenameError(error) bool { return false }
