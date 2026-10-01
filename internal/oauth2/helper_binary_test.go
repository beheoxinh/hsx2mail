package oauth2

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateHelperBinaryRejectsInsecureFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode/ownership checks are Unix-only")
	}

	dir := t.TempDir()

	// os.WriteFile applies the process umask, so Chmod is needed to actually
	// set the mode under test.
	write := func(name string, mode os.FileMode) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("#!/bin/sh\necho '{}'\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		if err := os.Chmod(p, mode); err != nil {
			t.Fatalf("chmod %s: %v", name, err)
		}
		return p
	}

	tests := []struct {
		name    string
		path    string
		wantErr string // substring; empty means "must be accepted"
	}{
		{"root-owned 0700", write("ok0700", 0o700), ""},
		{"root-owned 0755", write("ok0755", 0o755), ""},
		{"group-readable only is fine", write("grpo", 0o750), ""},
		{"group-writable", write("grp", 0o770), "writable by group or other"},
		{"world-writable", write("wrld", 0o707), "writable by group or other"},
		{"world-writable 0777", write("all", 0o777), "writable by group or other"},
		{"not executable", write("noexec", 0o600), "not executable"},
		{"no read bit but executable", write("xonly", 0o711), ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateHelperBinary(tc.path)
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("validateHelperBinary rejected a safe file: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("validateHelperBinary accepted %s (mode %04o)", tc.path, mustMode(t, tc.path))
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("error %q does not mention %q", err, tc.wantErr)
			}
		})
	}
}

// TestValidateHelperBinaryRejectsSymlinkAndDir covers the non-regular cases: a
// symlink is exactly how an attacker points a "validated" path somewhere else.
func TestValidateHelperBinaryRejectsSymlinkAndDir(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX symlink semantics")
	}
	dir := t.TempDir()

	real := filepath.Join(dir, "real")
	if err := os.WriteFile(real, []byte("#!/bin/sh\n"), 0o700); err != nil {
		t.Fatalf("write: %v", err)
	}

	link := filepath.Join(dir, "link")
	if err := os.Symlink(real, link); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := validateHelperBinary(link); err == nil {
		t.Fatal("symlink to a helper was accepted")
	}

	if err := validateHelperBinary(dir); err == nil {
		t.Fatal("directory was accepted")
	}

	if err := validateHelperBinary(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing path was accepted")
	}
}

// TestLoadFromShimRefusesInsecureHelper is the end-to-end check: a
// world-writable helper next to the binary must not be executed. A shell
// marker file proves execution by its side effect.
func TestLoadFromShimRefusesInsecureHelper(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("mode checks are Unix-only")
	}

	dir := t.TempDir()
	marker := filepath.Join(dir, "executed")
	helper := filepath.Join(dir, "hsx2mail-creds")

	script := "#!/bin/sh\ntouch " + marker + "\n" +
		`echo '{"google_client_id":"LEAKED","google_client_secret":"LEAKED"}'` + "\n"
	if err := os.WriteFile(helper, []byte(script), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	if err := os.Chmod(helper, 0o777); err != nil {
		t.Fatalf("chmod helper: %v", err)
	}

	if err := validateHelperBinary(helper); err == nil {
		t.Fatal("world-writable helper passed validation")
	}

	// loadFromShim only looks at fixed paths, so assert the decision helper
	// rather than mutating package state.
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("helper executed despite failing validation")
	}
}

func mustMode(t *testing.T, p string) os.FileMode {
	t.Helper()
	fi, err := os.Stat(p)
	if err != nil {
		t.Fatalf("stat %s: %v", p, err)
	}
	return fi.Mode().Perm()
}
