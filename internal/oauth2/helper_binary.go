package oauth2

import (
	"fmt"
	"os"
	"runtime"
)

// validateHelperBinary decides whether it is safe to exec the credential
// helper. A helper that decides which OAuth client the app talks to is a
// privilege boundary: if anyone who can write the file can also run it, then a
// download in ~/Downloads, an extracted AppImage, or a world-writable /tmp
// entry is enough to redirect every OAuth token exchange.
//
// Required, in order:
//
//   - it must be a regular file (no symlink, no directory, no device);
//   - on Unix it must be owned by the current uid or root;
//   - it must not be writable by group or other, and must carry at least one
//     execute bit.
//
// Windows named-pipe ACLs cover the equivalent ground there, so the ownership
// and mode checks are Unix-only; see helper_binary_windows.go.
func validateHelperBinary(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	if !info.Mode().IsRegular() {
		return fmt.Errorf("not a regular file (mode %s)", info.Mode())
	}

	if runtime.GOOS == "windows" {
		return nil
	}

	if err := checkHelperOwnership(info); err != nil {
		return err
	}

	perm := info.Mode().Perm()
	if perm&0o022 != 0 {
		return fmt.Errorf("writable by group or other (mode %04o)", perm)
	}
	if perm&0o111 == 0 {
		return fmt.Errorf("not executable (mode %04o)", perm)
	}

	return nil
}
