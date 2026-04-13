#!/usr/bin/env bash
# Drives every supported event type through the running test stack so you
# can cross-check what the exporter writes to
# ./output/openldap-events-$(date -u +%F).jsonl.
#
# Runs inside the "tools" sidecar (alpine + openldap-clients), so the host
# does not need ldap-utils installed. Assumes ./setup.sh has been executed
# and that "docker compose up -d" is running.

set -uo pipefail

TOOLS=${TOOLS:-openldap-events-test-tools}
LDAP_URL=${LDAP_URL:-ldap://openldap:389}
CONFIG_DN=${CONFIG_DN:-cn=adminconfig,cn=config}
CONFIG_PW=${CONFIG_PW:-adminpasswordconfig}
DATA_ADMIN_DN=${DATA_ADMIN_DN:-cn=admin,ou=users,dc=example,dc=org}
DATA_ADMIN_PW=${DATA_ADMIN_PW:-adminpassword}

ALICE_DN="cn=alice.tester,ou=users,dc=example,dc=org"
ALICE_PW="AliceCorrectHorse42"
BOB_DN="cn=bob.tester,ou=users,dc=example,dc=org"
BOB_PW="BobCorrectHorse42"
CAROL_DN="cn=carol.tester,ou=users,dc=example,dc=org"
CAROL_PW="CarolCorrectHorse42"

section() { printf '\n==== %s ====\n' "$*"; }
step()    { printf '[step] %s\n' "$*"; }

exec_in_tools() {
  docker exec -i "$TOOLS" "$@"
}

wait_for_tools() {
  step "Waiting for tools sidecar to be ready"
  for i in $(seq 1 30); do
    if docker exec "$TOOLS" sh -c 'command -v ldapsearch >/dev/null 2>&1'; then
      return 0
    fi
    sleep 1
  done
  echo "ERROR: ldap clients not available in $TOOLS" >&2
  exit 1
}

wait_for_ldap() {
  step "Waiting for slapd on $LDAP_URL"
  for i in $(seq 1 30); do
    if exec_in_tools ldapsearch -x -H "$LDAP_URL" -b "" -s base namingContexts >/dev/null 2>&1; then
      return 0
    fi
    sleep 1
  done
  echo "ERROR: slapd not reachable at $LDAP_URL" >&2
  exit 1
}

bind_as() {
  local dn=$1 pw=$2
  exec_in_tools ldapwhoami -x -H "$LDAP_URL" -D "$dn" -w "$pw" >/dev/null 2>&1
}

wait_for_tools
wait_for_ldap

section "1. Successful bind (expect bind.success for alice)"
if bind_as "$ALICE_DN" "$ALICE_PW"; then
  step "alice bind OK"
else
  step "alice bind FAILED — stopping"
  exit 1
fi

section "2. Failed binds (expect bind.failure + account.lock after 3 wrong tries for bob)"
for i in 1 2 3 4; do
  step "wrong-password bind #$i for bob"
  bind_as "$BOB_DN" "wrong-password-$i" || true
done

section "3. Password change (expect write.modify + password.change for carol)"
cat <<LDIF | exec_in_tools ldapmodify -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW"
dn: $CAROL_DN
changetype: modify
replace: userPassword
userPassword: CarolNewHorse99
LDIF

section "4. Password reset required (expect password.reset_required + write.modify on bob)"
# Data admin (cn=admin,ou=users) has write on ou=users; cn=adminconfig only
# has read on that subtree (ACL {2}), so binding as adminconfig would be
# rejected with "Insufficient access".
# "replace" is idempotent: safe to re-run without tripping
# attributeOrValueExists (20) when the attribute already carries the value.
cat <<LDIF | exec_in_tools ldapmodify -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW"
dn: $BOB_DN
changetype: modify
replace: pwdReset
pwdReset: TRUE
LDIF

section "5. Account unlock on bob (expect account.unlock)"
cat <<LDIF | exec_in_tools ldapmodify -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW"
dn: $BOB_DN
changetype: modify
delete: pwdAccountLockedTime
LDIF

section "6. write.add (expect write.add on new user dave)"
cat <<LDIF | exec_in_tools ldapadd -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW"
dn: cn=dave.tester,ou=users,dc=example,dc=org
objectClass: inetOrgPerson
cn: dave.tester
uid: dave.tester
sn: Tester
givenName: Dave
displayName: Dave Tester
mail: dave.tester@example.org
userPassword: DaveCorrectHorse42
LDIF

section "7. write.modify (expect write.modify on dave — mail attribute)"
cat <<LDIF | exec_in_tools ldapmodify -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW"
dn: cn=dave.tester,ou=users,dc=example,dc=org
changetype: modify
replace: mail
mail: dave.updated@example.org
LDIF

section "8. write.modrdn (expect write.modrdn on dave rename)"
exec_in_tools ldapmodrdn -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW" \
  "cn=dave.tester,ou=users,dc=example,dc=org" "cn=dave.renamed"

section "9. write.delete (expect write.delete on dave)"
exec_in_tools ldapdelete -x -H "$LDAP_URL" -D "$DATA_ADMIN_DN" -w "$DATA_ADMIN_PW" \
  "cn=dave.renamed,ou=users,dc=example,dc=org"

section "Done"
printf 'All operations issued. Wait for the next exporter scan (5s by default) and inspect:\n'
printf '  tail -f ./output/openldap-events-$(date -u +%%F).jsonl\n'
