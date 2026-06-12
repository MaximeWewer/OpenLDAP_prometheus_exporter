#!/usr/bin/env bash
# Bootstraps the tests/docker/data/ tree used by docker-compose.yml.
#
# cleanstart/openldap:2.6.13 is a minimal image with no entrypoint helper,
# so the slapd.d directory has to be pre-populated offline with slapadd
# before "docker compose up" can start slapd. This script runs slapadd in a
# throwaway container, then chowns the result to the image's UID/GID (101:102)
# with a helper alpine container.
#
# Usage:
#   ./setup.sh             # initial bootstrap (fails if data/ already exists)
#   ./setup.sh --reset     # wipe data/ and re-seed from scratch

set -euo pipefail

cd "$(dirname "$0")"

IMAGE="cleanstart/openldap:2.6.13"
HELPER_IMAGE="alpine:3.23"
LDAP_UID=101
LDAP_GID=102

DATA_ROOT="./data"
SLAPD_DIR="${DATA_ROOT}/slapd.d"
DATA_DIR="${DATA_ROOT}/openldap-data"
ACCESSLOG_DIR="${DATA_ROOT}/accesslog-data"

log() { printf '[setup] %s\n' "$*"; }

# --- TLS certificates ----------------------------------------------------
# slapd's cn=config references olcTLSCertificateFile/KeyFile/CACertificateFile
# under ./certs and the image entrypoint listens on ldaps://, so slapd will
# refuse to start if those files are missing. The certs are gitignored (they
# hold a private key), so generate them here when absent — this makes both
# local `./setup.sh` and CI work without committing key material.
if [[ ! -f ./certs/server.crt ]]; then
  log "Generating TLS certificates (./certs/gen-certs.sh)"
  ./certs/gen-certs.sh >/dev/null
fi

# --- reset handling ------------------------------------------------------
if [[ -d "$SLAPD_DIR" ]] && [[ -n "$(ls -A "$SLAPD_DIR" 2>/dev/null || true)" ]]; then
  if [[ "${1:-}" == "--reset" ]]; then
    log "Wiping existing data/ tree and previous events output"
    # Stop the stack first so the exporter does not hold an open handle
    # on output/*.jsonl — deleting while a process writes to the file
    # would leave it writing to an unlinked inode and the rotated file
    # never appears on disk.
    docker compose down 2>/dev/null || true
    docker run --rm --user root \
      -v "$(pwd)/${DATA_ROOT}:/data" \
      -v "$(pwd)/output:/output" \
      "$HELPER_IMAGE" \
      sh -c 'rm -rf /data/slapd.d/* /data/openldap-data/* /data/accesslog-data/* /output/*.jsonl /output/*.jsonl.*'
  else
    log "ERROR: $SLAPD_DIR is not empty."
    log "Run './setup.sh --reset' to wipe and reinitialize."
    exit 1
  fi
fi

mkdir -p "$SLAPD_DIR" "$DATA_DIR" "$ACCESSLOG_DIR" ./output

VOLUMES=(
  -v "$(pwd)/${SLAPD_DIR}:/etc/openldap/slapd.d"
  -v "$(pwd)/${DATA_DIR}:/var/lib/openldap/openldap-data"
  -v "$(pwd)/${ACCESSLOG_DIR}:/var/lib/openldap/accesslog-data"
)

# --- Step 1: seed cn=config ---------------------------------------------
log "Seeding cn=config via offline slapadd"
docker run --rm --user root \
  "${VOLUMES[@]}" \
  -v "$(pwd)/init-config:/init-config:ro" \
  --entrypoint slapadd "$IMAGE" \
  -n 0 -F /etc/openldap/slapd.d -l /init-config/slapd-config.ldif

# --- Step 2: seed main DB ------------------------------------------------
log "Seeding main database"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT
{
  for ldif in \
    init-ldifs/01-base.ldif \
    init-ldifs/02-org-ou.ldif \
    init-ldifs/03-users.ldif \
    init-ldifs/04-default-ppolicy.ldif; do
    sed 's/\r$//' "$ldif"
    printf '\n\n'
  done
} > "${TMP_DIR}/all-data.ldif"

docker run --rm --user root \
  "${VOLUMES[@]}" \
  -v "${TMP_DIR}:/init-data:ro" \
  --entrypoint slapadd "$IMAGE" \
  -n 1 -F /etc/openldap/slapd.d -l /init-data/all-data.ldif

# --- Step 3: fix permissions so slapd can read its own config/data ------
# chown to the cleanstart image UID/GID so slapd can open its config and
# backing stores, then chmod a+rX so the host user (and `go test ./...`
# walking the tree) can still traverse the directories and read files.
log "Fixing ownership to ${LDAP_UID}:${LDAP_GID} (and a+rX so the host user can read)"
docker run --rm --user root \
  "${VOLUMES[@]}" \
  "$HELPER_IMAGE" \
  sh -c "chown -R ${LDAP_UID}:${LDAP_GID} /etc/openldap/slapd.d /var/lib/openldap/openldap-data /var/lib/openldap/accesslog-data && chmod -R a+rX /etc/openldap/slapd.d /var/lib/openldap/openldap-data /var/lib/openldap/accesslog-data"

# --- Step 4: make ./output writable by the distroless/nonroot exporter ---
# The exporter image runs as UID 65532 (distroless/static:nonroot), which
# cannot write to the host-owned bind mount without a world-writable bit.
log "Opening ./output permissions for distroless nonroot (UID 65532)"
docker run --rm --user root \
  -v "$(pwd)/output:/output" \
  "$HELPER_IMAGE" \
  sh -c 'chmod 0777 /output'

log "Seed complete."
log "Next steps:"
log "  docker compose up -d --build"
log "  ./scripts/trigger-events.sh"
log "  tail -f output/openldap-events-\$(date -u +%F).jsonl"
