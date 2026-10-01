// Package oauth2 provides OAuth2 authentication for email providers
package oauth2

import (
	"encoding/json"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Build-time variables injected via ldflags
// These are set during compilation using:
//
//	go build -ldflags "-X 'github.com/beheoxinh/hsx2mail/internal/oauth2.GoogleClientID=xxx'"
//
// See Makefile for the complete build command.
// If ldflags are not set, credentials are loaded from the hsx2mail-creds shim binary.
var (
	// GoogleClientID is the OAuth2 client ID for Google/Gmail (Mail-scoped project).
	// Same client also backs first-party extensions' Google flows for any scopes
	// listed in the extension manifest's first_party_uses_core_for_scopes (today:
	// contacts.readonly). When that's not enough (write scopes, full Calendar),
	// the picker UI offers GoogleTestingClientID instead — see below.
	GoogleClientID string

	// GoogleClientSecret is the OAuth2 client secret for Google/Gmail
	GoogleClientSecret string

	// MicrosoftClientID is the OAuth2 client ID for Microsoft/Outlook
	// (Mail-scoped registration). Also serves microsoft-contacts and
	// microsoft-calendar — Microsoft Graph doesn't gate scopes behind
	// verification, so one app registration covers all three surfaces.
	MicrosoftClientID string

	// GoogleTestingClientID is the shared OAuth2 client for first-party
	// extensions that need broader Google scopes than the mail project is
	// verified for (e.g., contacts.readwrite, full Calendar). Single un-
	// Google-verified test client backs both google-contacts and
	// google-calendar slots. Surfaced in the picker as
	// "Email Hub - Google (Testing)" so users understand the verification
	// status before consenting. When the mail project eventually gets
	// verified with these scopes, the default in the picker UI switches
	// to "Email Hub - Google" (which reuses GoogleClientID via a manifest-
	// declared scope route) and this slot becomes a fallback.
	GoogleTestingClientID string

	// GoogleTestingClientSecret pairs with GoogleTestingClientID.
	GoogleTestingClientSecret string
)

func init() {
	if GoogleClientID != "" {
		return
	}
	loadFromShim()
	if GoogleClientID == "" {
		loadFromEnvFile()
	}
}

// loadFromEnvFile reads OAuth credentials from the project root .env when
// running un-stripped (go run / go test / IDE run config). Production builds
// carry ldflags and never reach this path. The fallback keeps dev and test
// behavior consistent with `make build` without leaking secrets into the
// repository — .env is gitignored (see .gitignore).
func loadFromEnvFile() {
	// Walk up to a few parent directories to find the repo root .env.
	// go test runs the test binary with the package dir as cwd, so the
	// .env can sit one or two levels up; the IDE run config uses the
	// project root as cwd, so it sits right here.
	for _, dir := range rootCandidates() {
		lines, err := os.ReadFile(filepath.Join(dir, ".env"))
		if err != nil {
			continue
		}
		applyEnvLines(lines)
		return
	}
}

// rootCandidates returns the current working directory and up to two
// ancestors, in order of most specific to least.
func rootCandidates() []string {
	dir, err := os.Getwd()
	if err != nil {
		return nil
	}
	var out []string
	for i := 0; i < 3; i++ {
		out = append(out, dir)
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return out
}

// applyEnvLines parses key=value lines (skipping comments/blanks) and fills
// the package credential vars when they are still empty.
func applyEnvLines(lines []byte) {
	for _, line := range strings.Split(string(lines), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		kv := strings.SplitN(line, "=", 2)
		if len(kv) != 2 {
			continue
		}
		key, val := strings.TrimSpace(kv[0]), strings.TrimSpace(kv[1])
		val = strings.Trim(val, `"'`)
		switch key {
		case "GOOGLE_CLIENT_ID":
			if GoogleClientID == "" {
				GoogleClientID = val
			}
		case "GOOGLE_CLIENT_SECRET":
			if GoogleClientSecret == "" {
				GoogleClientSecret = val
			}
		case "MICROSOFT_CLIENT_ID":
			if MicrosoftClientID == "" {
				MicrosoftClientID = val
			}
		case "GOOGLE_TESTING_CLIENT_ID":
			if GoogleTestingClientID == "" {
				GoogleTestingClientID = val
			}
		case "GOOGLE_TESTING_CLIENT_SECRET":
			if GoogleTestingClientSecret == "" {
				GoogleTestingClientSecret = val
			}
		}
	}
}

// shimCandidatePaths lists where the OAuth credential helper may live.
//
// Only paths that actually exist are returned. The Flatpak location is added
// conditionally because including it unconditionally made every launch on a
// normal install log "oauth2: refusing to run credential helper
// /app/lib/hsx2mail/hsx2mail-creds: no such file or directory" — a warning that
// reads like a failure even though the co-located helper loads successfully
// right afterwards.
func shimCandidatePaths() []string {
	var paths []string

	const flatpakHelper = "/app/lib/hsx2mail/hsx2mail-creds"
	if _, err := os.Stat(flatpakHelper); err == nil {
		paths = append(paths, flatpakHelper)
	}

	// Next to the main binary — how install.sh lays it out.
	if exe, err := os.Executable(); err == nil {
		paths = append(paths, filepath.Join(filepath.Dir(exe), "hsx2mail-creds"))
	}
	return paths
}

func loadFromShim() {
	paths := shimCandidatePaths()

	for _, p := range paths {
		if err := validateHelperBinary(p); err != nil {
			// Refuse to exec a helper we cannot vouch for. Silently continuing
			// would either skip a valid helper or, worse, run an attacker-planted
			// one that decides which OAuth client the app uses.
			log.Printf("oauth2: refusing to run credential helper %s: %v", p, err)
			continue
		}
		out, err := exec.Command(p).Output()
		if err != nil {
			continue
		}
		var creds map[string]string
		if err := json.Unmarshal(out, &creds); err != nil {
			continue
		}
		GoogleClientID = creds["google_client_id"]
		GoogleClientSecret = creds["google_client_secret"]
		MicrosoftClientID = creds["microsoft_client_id"]
		// Optional shared testing client for the un-verified Google
		// project that backs extensions needing broader scopes. Empty
		// until provisioned — picker simply omits the "(Testing)" option.
		GoogleTestingClientID = creds["google_testing_client_id"]
		GoogleTestingClientSecret = creds["google_testing_client_secret"]
		return
	}
}

// IsGoogleConfigured returns true if Google OAuth credentials are
// available from ANY configured source — user override (Settings → OAuth
// Credentials), a user-set slot alias, or the shipped build-time vars.
// Routed through the resolver so a from-source build with empty
// build-time creds but a user override saved in the UI still passes the
// pre-flight check at the start of the OAuth flow.
func IsGoogleConfigured() bool {
	creds, ok := ClientConfigForID("google-mail")
	return ok && creds.ClientID != ""
}

// IsMicrosoftConfigured mirrors IsGoogleConfigured for Microsoft.
func IsMicrosoftConfigured() bool {
	creds, ok := ClientConfigForID("microsoft-mail")
	return ok && creds.ClientID != ""
}

// IsProviderConfigured returns true if the specified provider has OAuth credentials
func IsProviderConfigured(provider string) bool {
	switch provider {
	case "google":
		return IsGoogleConfigured()
	case "microsoft":
		return IsMicrosoftConfigured()
	default:
		return false
	}
}
