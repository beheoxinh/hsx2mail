package pgp

import (
	"bytes"
	"fmt"
	"strings"
	"time"

	"github.com/ProtonMail/go-crypto/openpgp"
	"github.com/ProtonMail/go-crypto/openpgp/armor"
	"github.com/ProtonMail/go-crypto/openpgp/packet"
)

// ParseArmoredKey parses an ASCII-armored PGP key (public or private)
func ParseArmoredKey(armored string) (openpgp.EntityList, error) {
	entities, err := openpgp.ReadArmoredKeyRing(strings.NewReader(armored))
	if err != nil {
		return nil, fmt.Errorf("failed to parse armored key: %w", err)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("no keys found in armored data")
	}
	return entities, nil
}

// ParseBinaryKey parses a binary (non-armored) PGP key
func ParseBinaryKey(data []byte) (openpgp.EntityList, error) {
	entities, err := openpgp.ReadKeyRing(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse binary key: %w", err)
	}
	if len(entities) == 0 {
		return nil, fmt.Errorf("no keys found in binary data")
	}
	return entities, nil
}

// ParseKeyAuto auto-detects format and parses a PGP key from raw bytes
func ParseKeyAuto(data []byte) (openpgp.EntityList, error) {
	// Try armored first
	entities, err := ParseArmoredKey(string(data))
	if err == nil {
		return entities, nil
	}

	// Fall back to binary
	return ParseBinaryKey(data)
}

// ExtractKeyMetadata extracts metadata from a PGP entity into a Key struct
func ExtractKeyMetadata(entity *openpgp.Entity) *Key {
	pk := entity.PrimaryKey

	key := &Key{
		KeyID:       fmt.Sprintf("%016X", pk.KeyId),
		Fingerprint: fmt.Sprintf("%X", pk.Fingerprint),
		Algorithm:   algorithmName(pk.PubKeyAlgo),
		KeySize:     keyBitLength(pk),
	}

	createdAt := pk.CreationTime
	key.CreatedAtKey = &createdAt

	// Extract user ID and email
	for _, ident := range entity.Identities {
		key.UserID = ident.Name
		if ident.UserId != nil && ident.UserId.Email != "" {
			key.Email = ident.UserId.Email
		}
		// Check expiration from self-signature
		if ident.SelfSignature != nil && ident.SelfSignature.KeyLifetimeSecs != nil {
			expiry := pk.CreationTime.Add(time.Duration(*ident.SelfSignature.KeyLifetimeSecs) * time.Second)
			key.ExpiresAtKey = &expiry
		}
		break // Use first identity
	}

	// Check if key is expired
	key.IsExpired = IsKeyExpired(entity)

	// Check if entity has a private key
	key.HasPrivate = entity.PrivateKey != nil

	return key
}

// KeyFingerprint returns the hex fingerprint of a PGP entity
func KeyFingerprint(entity *openpgp.Entity) string {
	return fmt.Sprintf("%X", entity.PrimaryKey.Fingerprint)
}

// ExtractEmailFromKey extracts the email address from the first identity of a PGP entity
func ExtractEmailFromKey(entity *openpgp.Entity) string {
	for _, ident := range entity.Identities {
		if ident.UserId != nil && ident.UserId.Email != "" {
			return ident.UserId.Email
		}
	}
	return ""
}

// IsKeyExpired checks if a PGP entity's primary key is expired
func IsKeyExpired(entity *openpgp.Entity) bool {
	now := time.Now()
	for _, ident := range entity.Identities {
		if ident.SelfSignature != nil && ident.SelfSignature.KeyLifetimeSecs != nil {
			expiry := entity.PrimaryKey.CreationTime.Add(
				time.Duration(*ident.SelfSignature.KeyLifetimeSecs) * time.Second,
			)
			if now.After(expiry) {
				return true
			}
		}
		break
	}
	return false
}

// algorithmName returns a human-readable name for a public key algorithm
func algorithmName(algo packet.PublicKeyAlgorithm) string {
	switch algo {
	case packet.PubKeyAlgoRSA, packet.PubKeyAlgoRSASignOnly, packet.PubKeyAlgoRSAEncryptOnly:
		return "RSA"
	case packet.PubKeyAlgoDSA:
		return "DSA"
	case packet.PubKeyAlgoElGamal:
		return "ElGamal"
	case packet.PubKeyAlgoECDSA:
		return "ECDSA"
	case packet.PubKeyAlgoEdDSA:
		return "EdDSA"
	case packet.PubKeyAlgoECDH:
		return "ECDH"
	default:
		return fmt.Sprintf("Unknown(%d)", algo)
	}
}

// keyBitLength returns the bit length of a public key
func keyBitLength(pk *packet.PublicKey) int {
	bitLen, err := pk.BitLength()
	if err != nil {
		return 0
	}
	return int(bitLen)
}

// ArmorPublicKey exports a PGP entity's public key as ASCII-armored text
func ArmorPublicKey(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, "PGP PUBLIC KEY BLOCK", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor writer: %w", err)
	}
	if err := entity.Serialize(w); err != nil {
		return "", fmt.Errorf("failed to serialize public key: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("failed to close armor writer: %w", err)
	}
	return buf.String(), nil
}

// ArmorPrivateKey exports a PGP entity's private key as ASCII-armored text
func ArmorPrivateKey(entity *openpgp.Entity) (string, error) {
	var buf bytes.Buffer
	w, err := armor.Encode(&buf, "PGP PRIVATE KEY BLOCK", nil)
	if err != nil {
		return "", fmt.Errorf("failed to create armor writer: %w", err)
	}
	if err := entity.SerializePrivate(w, nil); err != nil {
		return "", fmt.Errorf("failed to serialize private key: %w", err)
	}
	if err := w.Close(); err != nil {
		return "", fmt.Errorf("failed to close armor writer: %w", err)
	}
	return buf.String(), nil
}
