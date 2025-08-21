package config

import (
	"crypto/cipher"
	"crypto/rand"
	"errors"

	"golang.org/x/crypto/chacha20poly1305"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/logger"
)

// SecureString provides cryptographically secure protection for sensitive strings in memory
// using ChaCha20-Poly1305 authenticated encryption
type SecureString struct {
	ciphertext []byte
	nonce      []byte
	aead       cipher.AEAD
}

// NewSecureString creates a new SecureString with ChaCha20-Poly1305 encryption
// This provides cryptographically secure protection with authentication
func NewSecureString(value string) (*SecureString, error) {
	if value == "" {
		return nil, errors.New("cannot create SecureString with empty value")
	}

	// Generate random 256-bit key for ChaCha20
	key := make([]byte, chacha20poly1305.KeySize)
	if _, err := rand.Read(key); err != nil {
		return nil, err
	}

	// Create ChaCha20-Poly1305 AEAD cipher
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		// Clear key from memory before returning error
		for i := range key {
			key[i] = 0
		}
		return nil, err
	}

	// Generate random nonce
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		// Clear sensitive data before returning error
		for i := range key {
			key[i] = 0
		}
		return nil, err
	}

	// Encrypt with authentication
	ciphertext := aead.Seal(nil, nonce, []byte(value), nil)

	// Clear the original key from local memory (AEAD keeps its own copy)
	for i := range key {
		key[i] = 0
	}

	return &SecureString{
		ciphertext: ciphertext,
		nonce:      nonce,
		aead:       aead,
	}, nil
}

// String returns the decrypted value
func (s *SecureString) String() string {
	if s == nil || len(s.ciphertext) == 0 || s.aead == nil {
		return ""
	}

	// Decrypt and verify authentication tag
	plaintext, err := s.aead.Open(nil, s.nonce, s.ciphertext, nil)
	if err != nil {
		// Authentication failed - data was tampered with
		logger.SafeError("secure", "SecureString authentication failed - possible tampering detected", err)
		return ""
	}

	// Convert to string and clear plaintext from memory
	result := string(plaintext)
	for i := range plaintext {
		plaintext[i] = 0
	}

	return result
}

// Clear securely clears the SecureString from memory
func (s *SecureString) Clear() {
	if s == nil {
		return
	}

	// Clear ciphertext
	for i := range s.ciphertext {
		s.ciphertext[i] = 0
	}

	// Clear nonce
	for i := range s.nonce {
		s.nonce[i] = 0
	}

	// Clear references
	s.ciphertext = nil
	s.nonce = nil
	s.aead = nil
}

// IsEmpty checks if the SecureString is empty or cleared
func (s *SecureString) IsEmpty() bool {
	return s == nil || len(s.ciphertext) == 0 || s.aead == nil
}
