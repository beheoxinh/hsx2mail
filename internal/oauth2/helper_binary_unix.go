//go:build !windows

package oauth2

import (
	"fmt"
	"io/fs"
	"os"
	"syscall"
)

// checkHelperOwnership requires the helper to belong to the running user or to
// root. A file owned by anybody else can be replaced by that owner at any time
// without going through this user's permissions.
func checkHelperOwnership(info fs.FileInfo) error {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fmt.Errorf("cannot read ownership")
	}
	if uid := os.Getuid(); st.Uid != uint32(uid) && st.Uid != 0 {
		return fmt.Errorf("owned by uid %d, want %d or root", st.Uid, uid)
	}
	return nil
}
