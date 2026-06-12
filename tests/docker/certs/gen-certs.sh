#!/usr/bin/env bash
# Generates a throwaway CA + server certificate for the TLS test stack.
# The server cert's CN/SAN is "openldap" (the compose service name) plus
# localhost/127.0.0.1, so the exporter can verify the chain against ca.crt
# while connecting to ldaps://openldap:636.
#
# Re-run to rotate the certs (e.g. to test the not_after metric changing).
set -euo pipefail
cd "$(dirname "$0")"

DAYS=825
SUBJ_CA="/CN=OpenLDAP Demo Test CA/O=Demo"
SUBJ_SRV="/CN=openldap/O=Demo"

# --- CA ------------------------------------------------------------------
openssl genrsa -out ca.key 4096
openssl req -x509 -new -nodes -key ca.key -sha256 -days $((DAYS * 2)) \
  -subj "$SUBJ_CA" -out ca.crt

# --- Server cert signed by the CA ---------------------------------------
openssl genrsa -out server.key 2048
openssl req -new -key server.key -subj "$SUBJ_SRV" -out server.csr

cat > server.ext <<'EOF'
basicConstraints = CA:FALSE
keyUsage = digitalSignature, keyEncipherment
extendedKeyUsage = serverAuth
subjectAltName = DNS:openldap, DNS:localhost, IP:127.0.0.1
EOF

openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -days "$DAYS" -sha256 -extfile server.ext -out server.crt

# slapd runs as the "ldap" user (UID 101); make the bind-mounted files
# world-readable so it can read them regardless of host ownership.
chmod 644 ca.crt server.crt server.key

rm -f server.csr server.ext ca.srl
echo "[gen-certs] done:"
openssl x509 -in server.crt -noout -subject -issuer -enddate -ext subjectAltName
