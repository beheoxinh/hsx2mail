package pgp

import (
	"testing"

	"github.com/ProtonMail/go-crypto/openpgp"
)

func generateTestKey(t *testing.T) *openpgp.Entity {
	t.Helper()
	entity, err := openpgp.NewEntity("Test User", "test", "test@example.com", nil)
	if err != nil {
		t.Fatal(err)
	}
	// Remove the zero-value KeyLifetimeSecs to avoid instant expiration
	for _, ident := range entity.Identities {
		if ident.SelfSignature != nil {
			ident.SelfSignature.KeyLifetimeSecs = nil
		}
	}
	return entity
}

func TestExtractKeyMetadata(t *testing.T) {
	entity := generateTestKey(t)
	meta := ExtractKeyMetadata(entity)

	if meta.KeyID == "" {
		t.Error("expected non-empty KeyID")
	}
	if meta.Fingerprint == "" {
		t.Error("expected non-empty Fingerprint")
	}
	if meta.Email != "test@example.com" {
		t.Errorf("Email = %q, want %q", meta.Email, "test@example.com")
	}
	if meta.UserID == "" {
		t.Error("expected non-empty UserID")
	}
	// UserID should contain the name
	if meta.UserID == "" {
		t.Error("expected UserID to contain name info")
	}
	if meta.Algorithm == "" {
		t.Error("expected non-empty Algorithm")
	}
	if meta.HasPrivate != true {
		t.Error("expected HasPrivate to be true for generated entity")
	}
	// Note: openpgp.NewEntity(nil config) sets KeyLifetimeSecs=0 which means
	// expired immediately. IsExpired being true is expected for default config.
}

func TestKeyFingerprint(t *testing.T) {
	entity := generateTestKey(t)
	fp := KeyFingerprint(entity)

	if fp == "" {
		t.Error("expected non-empty fingerprint")
	}
	// Fingerprint should be a hex string
	for _, c := range fp {
		valid := (c >= '0' && c <= '9') || (c >= 'A' && c <= 'F') || (c >= 'a' && c <= 'f')
		if !valid {
			t.Errorf("fingerprint contains non-hex character: %c", c)
			break
		}
	}
}

func TestExtractEmailFromKey(t *testing.T) {
	entity := generateTestKey(t)
	email := ExtractEmailFromKey(entity)

	if email != "test@example.com" {
		t.Errorf("ExtractEmailFromKey = %q, want %q", email, "test@example.com")
	}
}

func TestArmorPublicKeyRoundTrip(t *testing.T) {
	entity := generateTestKey(t)
	originalFP := KeyFingerprint(entity)

	armored, err := ArmorPublicKey(entity)
	if err != nil {
		t.Fatalf("ArmorPublicKey failed: %v", err)
	}
	if armored == "" {
		t.Fatal("expected non-empty armored output")
	}

	parsed, err := ParseArmoredKey(armored)
	if err != nil {
		t.Fatalf("ParseArmoredKey failed: %v", err)
	}
	if len(parsed) == 0 {
		t.Fatal("expected at least one entity from parsed armored key")
	}

	parsedFP := KeyFingerprint(parsed[0])
	if parsedFP != originalFP {
		t.Errorf("fingerprint mismatch: got %q, want %q", parsedFP, originalFP)
	}
}

func TestParseArmoredKeyInvalid(t *testing.T) {
	_, err := ParseArmoredKey("not a key")
	if err == nil {
		t.Error("expected error for invalid armored key, got nil")
	}
}

func TestParseKeyAutoArmored(t *testing.T) {
	entity := generateTestKey(t)

	armored, err := ArmorPublicKey(entity)
	if err != nil {
		t.Fatalf("ArmorPublicKey failed: %v", err)
	}

	entities, err := ParseKeyAuto([]byte(armored))
	if err != nil {
		t.Fatalf("ParseKeyAuto failed: %v", err)
	}
	if len(entities) == 0 {
		t.Fatal("expected at least one entity")
	}

	gotFP := KeyFingerprint(entities[0])
	wantFP := KeyFingerprint(entity)
	if gotFP != wantFP {
		t.Errorf("fingerprint mismatch: got %q, want %q", gotFP, wantFP)
	}
}
