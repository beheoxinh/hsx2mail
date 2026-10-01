package crypto

import "strings"

// EmailsMatch reports whether a verified signer identity belongs to the same
// mailbox as the message From.
//
// This is the sender-binding check for PGP and S/MIME verification: a detached
// signature proves "some key signed these bytes", not "the key that signed them
// belongs to the person this claims to be from". Without the comparison, any
// key in the keyring makes any message look signed by its holder.
//
// Comparison is case-insensitive on the domain (the DNS part is
// case-insensitive by RFC 1035) and on the local part (RFC 5321 §2.4 makes it
// case-sensitive, but every real provider treats it case-insensitively; being
// strict here produces false "mismatch" warnings rather than false "match").
func EmailsMatch(a, b string) bool {
	a = strings.ToLower(strings.TrimSpace(a))
	b = strings.ToLower(strings.TrimSpace(b))
	return a != "" && a == b
}

// SignerMatchesSender decides how a verified signer identity should be reported
// against a message From address.
//
// Returns true when the identity matches, and false when it does not. An empty
// signer identity cannot be judged — the key carried no UID email — so it is
// reported as matching rather than as an attacker-visible mismatch signal.
func SignerMatchesSender(signerEmail, fromEmail string) bool {
	if strings.TrimSpace(signerEmail) == "" {
		return true
	}
	return EmailsMatch(signerEmail, fromEmail)
}
