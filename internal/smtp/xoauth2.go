package smtp

import (
	"fmt"
	"net/smtp"
)

// AuthType represents the authentication method
type AuthType string

const (
	AuthTypePassword AuthType = "password"
	AuthTypeOAuth2   AuthType = "oauth2"
)

// xoauth2Auth implements smtp.Auth for the XOAUTH2 mechanism
// See: https://developers.google.com/gmail/imap/xoauth2-protocol
type xoauth2Auth struct {
	username string
	token    string
}

// XOAuth2Auth returns an smtp.Auth that implements the XOAUTH2 authentication mechanism
func XOAuth2Auth(username, token string) smtp.Auth {
	return &xoauth2Auth{
		username: username,
		token:    token,
	}
}

// Start begins the XOAUTH2 authentication and returns the mechanism name and initial response
func (a *xoauth2Auth) Start(server *smtp.ServerInfo) (string, []byte, error) {
	// Format: "user=" {user} "\x01" "auth=Bearer " {token} "\x01\x01"
	// Same format as IMAP XOAUTH2
	resp := fmt.Sprintf("user=%s\x01auth=Bearer %s\x01\x01", a.username, a.token)
	return "XOAUTH2", []byte(resp), nil
}

// Next processes server challenges
// XOAUTH2 sends everything in the initial response, so this handles error responses
func (a *xoauth2Auth) Next(fromServer []byte, more bool) ([]byte, error) {
	if more {
		// If server sends more data, it's an error response
		// Return empty to acknowledge and let the server send the final error
		return []byte{}, nil
	}
	return nil, nil
}
