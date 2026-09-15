package pgp

import (
	"crypto/sha1"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// LookupWKD performs a Web Key Directory lookup for a given email address.
// Returns the ASCII-armored public key if found, or empty string + nil error if not found.
func LookupWKD(email string) (string, error) {
	parts := strings.SplitN(email, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("invalid email address: %s", email)
	}

	localpart := strings.ToLower(parts[0])
	domain := strings.ToLower(parts[1])

	// z-base-32 encode SHA-1 hash of the localpart
	hash := sha1.Sum([]byte(localpart))
	encoded := zBase32Encode(hash[:])

	client := &http.Client{Timeout: 5 * time.Second}

	// Try direct method first: https://<domain>/.well-known/openpgpkey/hu/<hash>?l=<localpart>
	directURL := fmt.Sprintf("https://%s/.well-known/openpgpkey/hu/%s?l=%s", domain, encoded, localpart)
	armored, err := fetchWKD(client, directURL)
	if err == nil && armored != "" {
		return armored, nil
	}

	// Try advanced method: https://openpgpkey.<domain>/.well-known/openpgpkey/<domain>/hu/<hash>?l=<localpart>
	advancedURL := fmt.Sprintf("https://openpgpkey.%s/.well-known/openpgpkey/%s/hu/%s?l=%s", domain, domain, encoded, localpart)
	armored, err = fetchWKD(client, advancedURL)
	if err == nil && armored != "" {
		return armored, nil
	}

	return "", nil
}

// fetchWKD fetches a WKD URL and returns the key data as armored text
func fetchWKD(client *http.Client, url string) (string, error) {
	resp, err := client.Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, 1024*1024)) // 1MB limit
	if err != nil {
		return "", err
	}

	if len(data) == 0 {
		return "", fmt.Errorf("empty response")
	}

	// WKD returns binary key data; convert to armored
	entities, err := ParseBinaryKey(data)
	if err != nil {
		// Maybe it's already armored
		entities, err = ParseArmoredKey(string(data))
		if err != nil {
			return "", fmt.Errorf("failed to parse WKD response: %w", err)
		}
	}

	if len(entities) == 0 {
		return "", fmt.Errorf("no keys in WKD response")
	}

	return ArmorPublicKey(entities[0])
}

// zBase32Encode encodes bytes using z-base-32 encoding (RFC 6189)
func zBase32Encode(data []byte) string {
	const alphabet = "ybndrfg8ejkmcpqxot1uwisza345h769"

	var result strings.Builder
	buffer := 0
	bitsLeft := 0

	for _, b := range data {
		buffer = (buffer << 8) | int(b)
		bitsLeft += 8

		for bitsLeft >= 5 {
			bitsLeft -= 5
			index := (buffer >> bitsLeft) & 0x1F
			result.WriteByte(alphabet[index])
		}
	}

	if bitsLeft > 0 {
		index := (buffer << (5 - bitsLeft)) & 0x1F
		result.WriteByte(alphabet[index])
	}

	return result.String()
}
