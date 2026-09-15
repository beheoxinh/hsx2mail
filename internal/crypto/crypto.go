// Package crypto provides encryption utilities for secure credential storage
package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"golang.org/x/crypto/pbkdf2"
)

const (
	// keyFileName is the name of the file storing the encryption key
	keyFileName = "device.key"

	// saltSize is the size of the salt in bytes
	saltSize = 32

	// keySize is the size of the derived key in bytes (AES-256)
	keySize = 32

	// pbkdf2Iterations is the number of PBKDF2 iterations
	pbkdf2Iterations = 100000
)

// Encryptor provides AES-256-GCM encryption/decryption
type Encryptor struct {
	key []byte
}

// NewEncryptor creates a new Encryptor using a device-specific key
// The key is stored in the data directory and generated if it doesn't exist
func NewEncryptor(dataDir string) (*Encryptor, error) {
	keyPath := filepath.Join(dataDir, keyFileName)

	key, err := loadOrCreateKey(keyPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load or create key: %w", err)
	}

	return &Encryptor{key: key}, nil
}

// loadOrCreateKey loads the encryption key from disk, or creates a new one
func loadOrCreateKey(keyPath string) ([]byte, error) {
	// Try to read existing key
	data, err := os.ReadFile(keyPath)
	if err == nil && len(data) == keySize+saltSize {
		// Key file exists, derive key from stored salt and machine-specific data
		salt := data[:saltSize]
		return deriveKey(salt), nil
	}

	// Generate new key
	salt := make([]byte, saltSize)
	if _, err := io.ReadFull(rand.Reader, salt); err != nil {
		return nil, fmt.Errorf("failed to generate salt: %w", err)
	}

	key := deriveKey(salt)

	// Store salt (we can regenerate key from salt + machine data)
	keyData := make([]byte, saltSize+keySize)
	copy(keyData[:saltSize], salt)
	copy(keyData[saltSize:], key)

	// Create directory if needed
	if err := os.MkdirAll(filepath.Dir(keyPath), 0700); err != nil {
		return nil, fmt.Errorf("failed to create key directory: %w", err)
	}

	// Write key file with restricted permissions
	if err := os.WriteFile(keyPath, keyData, 0600); err != nil {
		return nil, fmt.Errorf("failed to write key file: %w", err)
	}

	return key, nil
}

// deriveKey derives an encryption key from salt and machine-specific data
func deriveKey(salt []byte) []byte {
	// Use machine-specific data as the base for key derivation
	// This includes hostname and user info for some uniqueness
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME")
	}

	// Combine machine-specific data
	machineData := fmt.Sprintf("hsx2mail:%s:%s:%d", hostname, username, os.Getuid())

	// Derive key using PBKDF2
	return pbkdf2.Key([]byte(machineData), salt, pbkdf2Iterations, keySize, sha256.New)
}

// Encrypt encrypts plaintext using AES-256-GCM
// Returns base64-encoded ciphertext (nonce prepended)
func (e *Encryptor) Encrypt(plaintext string) (string, error) {
	if plaintext == "" {
		return "", nil
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	// Generate random nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", fmt.Errorf("failed to generate nonce: %w", err)
	}

	// Encrypt and prepend nonce
	ciphertext := gcm.Seal(nonce, nonce, []byte(plaintext), nil)

	// Return base64-encoded result
	return base64.StdEncoding.EncodeToString(ciphertext), nil
}

// Decrypt decrypts base64-encoded ciphertext (with prepended nonce)
func (e *Encryptor) Decrypt(ciphertext string) (string, error) {
	if ciphertext == "" {
		return "", nil
	}

	// Decode base64
	data, err := base64.StdEncoding.DecodeString(ciphertext)
	if err != nil {
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	block, err := aes.NewCipher(e.key)
	if err != nil {
		return "", fmt.Errorf("failed to create cipher: %w", err)
	}

	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("failed to create GCM: %w", err)
	}

	nonceSize := gcm.NonceSize()
	if len(data) < nonceSize {
		return "", fmt.Errorf("ciphertext too short")
	}

	// Extract nonce and ciphertext
	nonce, ciphertextBytes := data[:nonceSize], data[nonceSize:]

	// Decrypt
	plaintext, err := gcm.Open(nil, nonce, ciphertextBytes, nil)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt: %w", err)
	}

	return string(plaintext), nil
}
