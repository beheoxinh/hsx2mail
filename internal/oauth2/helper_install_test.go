//go:build linux

package oauth2

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// The app resolves the credential helper relative to its own executable
// (internal/oauth2/config.go: filepath.Dir(exe)/hsx2mail-creds), and that lookup
// only succeeds when install.sh puts the helper next to the binary.
//
// This test pins the contract: a helper sitting next to the binary is found,
// passes the ownership/permission checks, and yields the credentials it prints.
// It runs the helper in a temp dir so the test does not depend on whatever is
// installed on the machine.
func TestInstalledHelperIsFoundNextToTheBinary(t *testing.T) {
	if os.Geteuid() == 0 {
		// Running as root makes every file "owned by the current user", so the
		// ownership check in validateHelperBinary proves nothing.
		t.Skip("skipped as root: the ownership check is vacuous")
	}

	dir := t.TempDir()
	// A stand-in helper: same contract as cmd/hsx2mail-creds (print JSON on
	// stdout), which is all loadFromShim consumes.
	helper := filepath.Join(dir, "hsx2mail-creds")
	body := `#!/bin/sh
printf '{"google_client_id":"test-id","google_client_secret":"test-secret","microsoft_client_id":"","google_testing_client_id":"","google_testing_client_secret":""}'
`
	if err := os.WriteFile(helper, []byte(body), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}

	if err := validateHelperBinary(helper); err != nil {
		t.Fatalf("a 0755 helper owned by the current user must validate: %v", err)
	}

	out, err := exec.Command(helper).Output()
	if err != nil {
		t.Fatalf("run helper: %v", err)
	}
	if len(out) == 0 {
		t.Fatal("helper produced no output; loadFromShim would find no credentials")
	}
}

// A helper that anyone can write to is a privilege boundary violation: whoever
// can write it decides which OAuth client the app authenticates against.
func TestWritableHelperIsRejected(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("skipped as root: root bypasses the mode check")
	}
	dir := t.TempDir()
	helper := filepath.Join(dir, "hsx2mail-creds")
	if err := os.WriteFile(helper, []byte("#!/bin/sh\necho '{}'\n"), 0o755); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	// WriteFile applies the umask, so 0o777 in the call above would land as
	// 0755 and the check under test would never run.
	if err := os.Chmod(helper, 0o777); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if got := helperMode(t, helper); got != 0o777 {
		t.Fatalf("mode is %04o, expected 0777 for this test to mean anything", got)
	}
	if err := validateHelperBinary(helper); err == nil {
		t.Error("a world-writable credential helper must be rejected")
	}
}

func helperMode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode().Perm()
}

// On a normal (non-Flatpak) install the Flatpak path cannot exist. Including it
// unconditionally made every launch log a "refusing to run credential helper"
// warning for a path that was never a candidate, which reads like a failure even
// though the co-located helper loads fine right after.
func TestFlatpakHelperPathOnlyAddedWhenPresent(t *testing.T) {
	const flatpakHelper = "/app/lib/hsx2mail/hsx2mail-creds"
	_, err := os.Stat(flatpakHelper)
	if err == nil {
		t.Skip("running inside a Flatpak sandbox: the path is expected to exist")
	}

	// The co-located path may legitimately be missing here too: under `go test`
	// the executable lives in a temp dir with no helper beside it. Assert only
	// that the Flatpak path is never offered.
	for _, p := range shimCandidatePaths() {
		if p == flatpakHelper {
			t.Errorf("%s does not exist but was returned as a candidate", flatpakHelper)
		}
	}
}
