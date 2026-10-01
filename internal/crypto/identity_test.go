package crypto

import "testing"

func TestEmailsMatch(t *testing.T) {
	tests := []struct {
		a, b string
		want bool
	}{
		{"user@example.test", "user@example.test", true},
		{"User@Example.Test", "user@example.test", true},
		{" user@example.test ", "user@example.test", true},
		{"user@example.test", "attacker@evil.test", false},
		{"user@example.test", "user@evil.test", false},
		{"user@example.test", "user+tag@example.test", false},
		{"user@example.test.evil.test", "user@example.test", false},
		{"", "", false},
	}
	for _, tc := range tests {
		if got := EmailsMatch(tc.a, tc.b); got != tc.want {
			t.Errorf("EmailsMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

// TestSignerMatchesSender documents the one deliberate exception: a key with no
// UID email cannot be judged, so it is not flagged as a mismatch.
func TestSignerMatchesSender(t *testing.T) {
	tests := []struct {
		name, signer, from string
		want               bool
	}{
		{"same", "a@b.test", "a@b.test", true},
		{"different", "a@b.test", "attacker@evil.test", false},
		{"case-insensitive", "A@B.test", "a@b.test", true},
		{"signer unknown", "", "a@b.test", true},
		{"signer whitespace only", "   ", "a@b.test", true},
		{"from unknown", "a@b.test", "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SignerMatchesSender(tc.signer, tc.from); got != tc.want {
				t.Fatalf("SignerMatchesSender(%q, %q) = %v, want %v", tc.signer, tc.from, got, tc.want)
			}
		})
	}
}
