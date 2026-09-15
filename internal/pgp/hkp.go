package pgp

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultHKPServers is the default list of HKP key servers to query.
// keys.openpgp.org is listed first as it is email-verified and most trustworthy.
var DefaultHKPServers = []string{
	"https://keys.openpgp.org",
	"https://keyserver.ubuntu.com",
	"https://pgp.mit.edu",
}

// LookupHKP queries HKP key servers sequentially for the given email address.
// Returns the ASCII-armored public key if found, or empty string + nil error if not found.
// If servers is empty, DefaultHKPServers are used.
func LookupHKP(email string, servers []string) (string, error) {
	if !strings.Contains(email, "@") {
		return "", fmt.Errorf("invalid email address: %s", email)
	}

	if len(servers) == 0 {
		servers = DefaultHKPServers
	}

	client := &http.Client{Timeout: 5 * time.Second}

	for _, server := range servers {
		armored, err := fetchHKP(client, server, email)
		if err != nil {
			continue
		}
		if armored != "" {
			return armored, nil
		}
	}

	return "", nil
}

// fetchHKP performs a single HKP lookup against one server.
// Returns empty string + nil error for HTTP 404 (key not found).
func fetchHKP(client *http.Client, serverURL, email string) (string, error) {
	u := fmt.Sprintf("%s/pks/lookup?op=get&search=%s&options=mr",
		strings.TrimRight(serverURL, "/"),
		url.QueryEscape(email),
	)

	resp, err := client.Get(u)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return "", nil
	}
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d from %s", resp.StatusCode, serverURL)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB limit
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		return "", nil
	}

	// Validate that the response contains a parseable PGP key
	entities, err := ParseArmoredKey(string(data))
	if err != nil {
		return "", fmt.Errorf("failed to parse HKP response from %s: %w", serverURL, err)
	}

	if len(entities) == 0 {
		return "", nil
	}

	return ArmorPublicKey(entities[0])
}
