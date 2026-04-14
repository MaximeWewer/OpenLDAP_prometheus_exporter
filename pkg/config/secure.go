package config

import (
	"errors"
	"sync"
)

// SecureString wraps sensitive credential bytes with an explicit lifecycle
// and best-effort zeroing on Clear().
//
// Threat model:
//   - In-process memory scanning of a running, unlocked Go process is
//     NOT mitigated. Pure Go cannot lock pages or guarantee the GC will
//     not copy the bytes.
//   - Core dumps taken after Clear() see a zero buffer instead of the
//     original credential — assuming the caller disciplines itself and
//     releases every string derived via String() as well.
//   - Accidental logging / reflection / printf("%+v") still redacts
//     nothing: use WithPlaintext when the caller can accept []byte.
type SecureString struct {
	mu   sync.Mutex
	data []byte
}

// NewSecureString stores the provided value in a fresh byte buffer.
// The input string's backing array cannot be zeroed by us because Go
// strings are immutable; callers that load the value from a file or an
// environment variable should therefore clear their own buffers once the
// SecureString is built.
func NewSecureString(value string) (*SecureString, error) {
	if value == "" {
		return nil, errors.New("cannot create SecureString with empty value")
	}
	buf := make([]byte, len(value))
	copy(buf, value)
	return &SecureString{data: buf}, nil
}

// String returns the stored credential as a Go string. Because strings
// are immutable, the returned value lives on the garbage-collected heap
// until it is reaped — it cannot be wiped in place. This method exists
// purely for interoperability with APIs (e.g. ldap.Conn.Bind) that accept
// only a string. Prefer WithPlaintext whenever the caller can consume
// []byte, so the plaintext buffer can be zeroed as soon as fn returns.
func (s *SecureString) String() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		return ""
	}
	return string(s.data)
}

// WithPlaintext hands the decoded credential to fn as a short-lived byte
// slice and zeroes that slice when fn returns, regardless of whether fn
// panics. Callers that can work with []byte (for example direct socket
// writes) should prefer this API over String() so the plaintext footprint
// stays bounded to fn's execution.
func (s *SecureString) WithPlaintext(fn func([]byte)) {
	if s == nil || fn == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.data) == 0 {
		fn(nil)
		return
	}
	cp := make([]byte, len(s.data))
	copy(cp, s.data)
	defer func() {
		for i := range cp {
			cp[i] = 0
		}
	}()
	fn(cp)
}

// Clear zeroes the stored credential buffer and releases the underlying
// slice. Subsequent calls to String / WithPlaintext return the empty
// value and IsEmpty returns true.
func (s *SecureString) Clear() {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.data {
		s.data[i] = 0
	}
	s.data = nil
}

// IsEmpty reports whether the SecureString has no stored credential.
func (s *SecureString) IsEmpty() bool {
	if s == nil {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data) == 0
}
