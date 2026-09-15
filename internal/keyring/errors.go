package keyring

import "errors"

var (
	// ErrCredentialNotFound is returned when a credential is not found in the keyring
	ErrCredentialNotFound = errors.New("credential not found in keyring")
)
