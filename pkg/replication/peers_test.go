package replication

import (
	"reflect"
	"testing"

	"github.com/MaximeWewer/OpenLDAP_prometheus_exporter/pkg/config"
)

func TestParseProviderURI(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{
			name: "indexed value with credentials",
			in:   `{0}rid=001 provider=ldaps://node2.example.com:636 searchbase="dc=example,dc=com" bindmethod=simple binddn="cn=replicator,dc=example,dc=com" credentials=secret`,
			want: "ldaps://node2.example.com:636",
		},
		{
			name: "provider not first token, quoted",
			in:   `rid=002 type=refreshAndPersist provider="ldap://100.81.3.39:389" retry="60 +"`,
			want: "ldap://100.81.3.39:389",
		},
		{name: "no provider", in: `rid=003 searchbase="dc=x"`, want: ""},
		{name: "empty", in: "", want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := parseProviderURI(tt.in); got != tt.want {
				t.Errorf("parseProviderURI() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestIsLDAPS(t *testing.T) {
	cases := map[string]bool{
		"ldaps://host:636": true,
		"LDAPS://host:636": true,
		" ldaps://host":    true,
		"ldap://host:389":  false,
		"http://host":      false,
	}
	for in, want := range cases {
		if got := isLDAPS(in); got != want {
			t.Errorf("isLDAPS(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestNormalizePeerURI(t *testing.T) {
	cases := map[string]string{
		"ldaps://Host:636":          "ldaps://host:636",
		" ldaps://host ":            "ldaps://host:636", // default ldaps port
		"ldap://host":               "ldap://host:389",  // default ldap port
		"LDAPS://NODE2.example.com": "ldaps://node2.example.com:636",
		"ldaps://[2001:db8::1]":     "ldaps://[2001:db8::1]:636",
		"":                          "",
		"   ":                       "",
	}
	for in, want := range cases {
		if got := normalizePeerURI(in); got != want {
			t.Errorf("normalizePeerURI(%q) = %q, want %q", in, got, want)
		}
	}
}

// discoverPeers with a nil local client falls back to the manual list, which it
// normalizes, de-duplicates and sorts. With no local client the result is
// authoritative (reliable == true).
func TestDiscoverPeersManualOnly(t *testing.T) {
	cfg := &config.Config{
		ReplicationPeers: []string{
			"ldaps://b.example.com:636",
			" ldaps://a.example.com ",   // no port -> :636
			"ldaps://B.example.com:636", // case-different duplicate of b
			"",                          // ignored
		},
	}

	got, reliable := discoverPeers(cfg, nil)
	want := []string{"ldaps://a.example.com:636", "ldaps://b.example.com:636"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("discoverPeers() = %v, want %v", got, want)
	}
	if !reliable {
		t.Errorf("discoverPeers() reliable = false, want true for manual-only")
	}
}
