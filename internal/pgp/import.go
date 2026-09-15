package pgp

import (
	"fmt"
)

// ImportKey imports a PGP key from raw data (armored or binary).
// If the key is encrypted with a passphrase, it decrypts it.
// Returns the armored private key, armored public key, key metadata, and any error.
func ImportKey(data []byte, passphrase string) (armoredPrivateKey, armoredPublicKey string, key *Key, err error) {
	entities, err := ParseKeyAuto(data)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to parse key: %w", err)
	}

	if len(entities) == 0 {
		return "", "", nil, fmt.Errorf("no keys found in data")
	}

	entity := entities[0]

	// Decrypt private key if encrypted
	if entity.PrivateKey != nil && entity.PrivateKey.Encrypted {
		if passphrase == "" {
			return "", "", nil, fmt.Errorf("private key is encrypted, passphrase required")
		}
		if err := entity.PrivateKey.Decrypt([]byte(passphrase)); err != nil {
			return "", "", nil, fmt.Errorf("failed to decrypt private key: %w", err)
		}
		// Also decrypt subkeys
		for _, subkey := range entity.Subkeys {
			if subkey.PrivateKey != nil && subkey.PrivateKey.Encrypted {
				if err := subkey.PrivateKey.Decrypt([]byte(passphrase)); err != nil {
					return "", "", nil, fmt.Errorf("failed to decrypt subkey: %w", err)
				}
			}
		}
	}

	// Extract metadata
	key = ExtractKeyMetadata(entity)

	// Armor public key
	armoredPublicKey, err = ArmorPublicKey(entity)
	if err != nil {
		return "", "", nil, fmt.Errorf("failed to armor public key: %w", err)
	}

	// Armor private key if present
	if entity.PrivateKey != nil {
		armoredPrivateKey, err = ArmorPrivateKey(entity)
		if err != nil {
			return "", "", nil, fmt.Errorf("failed to armor private key: %w", err)
		}
		key.HasPrivate = true
	}

	return armoredPrivateKey, armoredPublicKey, key, nil
}

// ImportPublicKey imports a public-only PGP key from raw data.
// Returns the armored public key, key metadata, and any error.
func ImportPublicKey(data []byte) (armoredPublicKey string, key *Key, err error) {
	entities, err := ParseKeyAuto(data)
	if err != nil {
		return "", nil, fmt.Errorf("failed to parse key: %w", err)
	}

	if len(entities) == 0 {
		return "", nil, fmt.Errorf("no keys found in data")
	}

	entity := entities[0]
	key = ExtractKeyMetadata(entity)

	armoredPublicKey, err = ArmorPublicKey(entity)
	if err != nil {
		return "", nil, fmt.Errorf("failed to armor public key: %w", err)
	}

	return armoredPublicKey, key, nil
}

