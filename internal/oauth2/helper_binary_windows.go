//go:build windows

package oauth2

import "io/fs"

// checkHelperOwnership is a no-op on Windows: there are no uid/mode bits, and
// the helper lives inside the install directory whose ACL is the boundary.
func checkHelperOwnership(fs.FileInfo) error { return nil }
