package imap

import "fmt"

// AuthType represents the authentication method
type AuthType string

const (
	AuthTypePassword AuthType = "password"
	AuthTypeOAuth2   AuthType = "oauth2"
)

// XOAuth2Client implements the XOAUTH2 SASL mechanism for IMAP
// See: https://developers.google.com/gmail/imap/xoauth2-protocol
//
// The XOAUTH2 mechanism sends a single response in the format:
// user={username}\x01auth=Bearer {token}\x01\x01
type XOAuth2Client struct {
	Username string
	Token    string
	done     bool
}

// NewXOAuth2Client creates a new XOAUTH2 SASL client
func NewXOAuth2Client(username, token string) *XOAuth2Client {
	return &XOAuth2Client{
		Username: username,
		Token:    token,
	}
}

// Start begins the SASL authentication and returns the mechanism name and initial response
func (c *XOAuth2Client) Start() (mech string, ir []byte, err error) {
	// Format: "user=" {user} "\x01" "auth=Bearer " {token} "\x01\x01"
	// \x01 is the separator byte
	ir = []byte(fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", c.Username, c.Token))
	return "XOAUTH2", ir, nil
}

// Next processes a server challenge and returns the client response
// XOAUTH2 doesn't have additional challenges in the success case
// If the server sends an error, it will be in the challenge
func (c *XOAuth2Client) Next(challenge []byte) ([]byte, error) {
	// If we've already sent the initial response and get a challenge,
	// it means authentication failed. The challenge contains error details.
	if c.done {
		// Return empty response to complete the failed exchange
		return nil, nil
	}
	c.done = true

	// If there's a challenge, it's an error from the server (base64-encoded JSON)
	// We return an empty response to signal we acknowledge the error
	// The IMAP client will then report the authentication failure
	if len(challenge) > 0 {
		return []byte{}, nil
	}

	return nil, nil
}
