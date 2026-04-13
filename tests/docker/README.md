# End-to-end test stack — OpenLDAP exporter events stream

Self-contained Docker stack used to validate the exporter's JSON events
stream (`OPENLDAP_EVENTS_ENABLED=true`). It boots a real OpenLDAP server
with the `slapo-accesslog` and `slapo-ppolicy` overlays, runs the exporter
image built from this repository, and provides a script that triggers
every supported event type so you can cross-check the file written to
`output/`.

## Layout

```
tests/docker/
├── docker-compose.yml           # openldap + exporter + tools sidecar
├── setup.sh                     # offline slapadd bootstrap (runs once)
├── init-config/
│   └── slapd-config.ldif        # full cn=config (modules, DBs, overlays, ACLs)
├── init-ldifs/
│   ├── 01-base.ldif             # dc=example,dc=org
│   ├── 02-org-ou.ldif           # ou=users, ou=policies
│   ├── 03-users.ldif            # admin + alice/bob/carol test users
│   └── 04-default-ppolicy.ldif  # pwdMaxFailure=3, pwdLockoutDuration=10m
├── scripts/
│   └── trigger-events.sh        # fires every supported event type
├── data/                        # runtime slapd.d + mdb files (gitignored)
└── output/                      # exporter writes openldap-events-YYYY-MM-DD.jsonl here
```

## Image

`cleanstart/openldap:2.6.13` — minimal OpenLDAP image with no bootstrap
helpers. Everything has to be pre-seeded into `slapd.d` via offline
`slapadd` before starting the container. This is identical to the setup
used by [`MaximeWewer/OpenLDAP-docker-setup`](https://github.com/MaximeWewer/OpenLDAP-docker-setup),
which served as the reference for the `slapd-config.ldif` contents.

The `tools` sidecar is a plain `alpine:3.23` container that installs
`openldap-clients` at startup and sleeps forever so `trigger-events.sh`
can invoke `ldapsearch` / `ldapmodify` / `ldapadd` / `ldapdelete` via
`docker exec` without requiring the host to have them installed.

## Credentials

| Role | DN | Password |
|------|----|----------|
| cn=config admin (bind DN used by the exporter) | `cn=adminconfig,cn=config` | `adminpasswordconfig` |
| main DB data admin (scripts) | `cn=admin,ou=users,dc=example,dc=org` | `adminpassword` |
| alice | `cn=alice.tester,ou=users,dc=example,dc=org` | `AliceCorrectHorse42` |
| bob | `cn=bob.tester,ou=users,dc=example,dc=org` | `BobCorrectHorse42` |
| carol | `cn=carol.tester,ou=users,dc=example,dc=org` | `CarolCorrectHorse42` |

The `cn=adminconfig,cn=config` password is pre-hashed in
`init-config/slapd-config.ldif` (reused verbatim from the reference repo).
Do **not** use these credentials outside this local test stack.

## Ports

| Host | Container | Service |
|------|-----------|---------|
| `1389` | `389` | OpenLDAP (no TLS) |
| `9330` | `9330` | Prometheus exporter (`/metrics`, `/health`) |

## Running the stack

```bash
cd tests/docker

# 1. Offline slapadd bootstrap — only needed once per reset
./setup.sh                # first time, or after removing data/
./setup.sh --reset        # wipe + re-seed

# 2. Build the exporter image and start every service
docker compose up -d --build

# 3. Verify the exporter sees slapd
curl -s http://localhost:9330/metrics | grep '^openldap_up'

# 4. Fire every supported event type
./scripts/trigger-events.sh

# 5. Watch the events stream (file rotates daily at UTC midnight)
tail -f output/openldap-events-$(date -u +%F).jsonl
```

The exporter is configured with `OPENLDAP_EVENTS_INTERVAL=5s` so events
land in the output file within ~5 seconds of the corresponding LDAP
operation.

## What `trigger-events.sh` does

The script issues operations that exercise every event type the exporter
can emit. Compare the order below against the JSON lines that appear in
`output/openldap-events-$(date -u +%F).jsonl`.

| Step | LDAP operation | Expected event(s) |
|------|----------------|-------------------|
| 1 | successful bind as alice | `bind.success` |
| 2 | 4 wrong-password binds as bob (pwdMaxFailure=3) | `bind.failure` (×4), `account.lock` (once the 3rd failure trips ppolicy) |
| 3 | replace userPassword on carol (as data admin) | `write.modify`, `password.change` |
| 4 | add `pwdReset=TRUE` on bob (as cn=config admin) | `write.modify`, `password.reset_required` |
| 5 | delete `pwdAccountLockedTime` on bob | `write.modify`, `account.unlock` |
| 6 | add a new user `dave.tester` | `write.add` |
| 7 | modify dave's mail attribute | `write.modify` |
| 8 | modrdn dave → dave.renamed | `write.modrdn` |
| 9 | delete dave.renamed | `write.delete` |

## Troubleshooting

**`./setup.sh` says the data directory is not empty.**
Run `./setup.sh --reset` to wipe it. This stops the stack first and deletes
everything under `data/`.

**`docker compose up` fails with a slapd permission error.**
The offline slapadd step chowns `data/` to UID/GID `101:102` (the UID the
cleanstart image runs as). If you edited files manually under `data/` you
may have to re-run `./setup.sh --reset`.

**Exporter logs `circuit breaker` / `connection refused`.**
The exporter starts before slapd is fully ready on the first boot. It
will reconnect automatically on the next scrape. If it does not, check
that port `1389` is free on the host and that `openldap` is in a running
state (`docker compose ps`).

**No events appear in `output/`.**
Check that `olcAccessLogOps: writes bind` is active
(`docker exec openldap-events-test-tools ldapsearch -x -H ldap://openldap:389 -D cn=adminconfig,cn=config -w adminpasswordconfig -b 'cn=config' '(olcOverlay=accesslog)'`)
and that the exporter container has `OPENLDAP_EVENTS_ENABLED=true` in its
environment (`docker inspect openldap-events-test-exporter | grep EVENTS`).

**Tear-down.**
`docker compose down -v` stops every service and removes named volumes.
`data/` and `output/` are bind mounts and survive unless you delete them
explicitly (`rm -rf data output/*.jsonl*`).
