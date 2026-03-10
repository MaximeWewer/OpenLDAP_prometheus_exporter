package exporter

import (
	"testing"
)

// FuzzValidateKey tests validateKey with random inputs
func FuzzValidateKey(f *testing.F) {
	// Seed corpus
	f.Add("")
	f.Add("valid-key")
	f.Add("connections_total")
	f.Add("a")
	f.Add("key\x00with\x00nulls")
	f.Add("key\nwith\nnewlines")
	f.Add("key\rwith\rreturns")
	f.Add(string(make([]byte, 1024))) // Very long key

	f.Fuzz(func(t *testing.T, key string) {
		err := validateKey(key)

		// Empty keys must always fail
		if key == "" && err == nil {
			t.Error("Empty key should return error")
		}

		// Keys over MaxKeyLength must always fail
		if len(key) > MaxKeyLength && err == nil {
			t.Error("Key exceeding MaxKeyLength should return error")
		}

		// Keys with null bytes must fail
		for _, r := range key {
			if r == 0 || r == '\r' || r == '\n' {
				if err == nil {
					t.Error("Key with invalid character should return error")
				}
				return
			}
		}
	})
}

// FuzzExtractCNFromFirstComponent tests CN extraction with random DNs
func FuzzExtractCNFromFirstComponent(f *testing.F) {
	f.Add("cn=Current,cn=Connections,cn=Monitor")
	f.Add("cn=Total,cn=Connections,cn=Monitor")
	f.Add("")
	f.Add("dc=example,dc=com")
	f.Add("cn=")
	f.Add("cn=test")
	f.Add("ou=People,dc=example,dc=com")
	f.Add("cn=Connection 0,cn=Connections,cn=Monitor")

	f.Fuzz(func(t *testing.T, dn string) {
		result := extractCNFromFirstComponent(dn)
		// Result should never panic
		_ = result
	})
}
