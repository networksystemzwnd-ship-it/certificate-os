#!/usr/bin/env bash
# Backup + restore rehearsal (M6b). Dumps a CertificateOS database, restores it into a throw-away database on the SAME
# server and proves the copy is faithful: same tables, row counts and content checksums, same row-level-security flags and policies, same
# triggers and runtime-role grants, and RLS still fails closed for the restricted role. Exit code 0 = restore verified.
#
#   PGHOST=... PGPORT=... PGUSER=... PGPASSWORD=... scripts/restore-check.sh <source_db> [dump_file]
#
# Use the OWNER/admin credentials (they need CREATE DATABASE). Never point this at production without a maintenance
# window: it reads the whole database once (pg_dump). The restored copy is dropped at the end; the dump file is kept
# only when you pass dump_file.
set -euo pipefail

SOURCE_DB="${1:?usage: restore-check.sh <source_db> [dump_file]}"
KEEP_DUMP="${2:-}"
DUMP="${KEEP_DUMP:-$(mktemp -t certos-restore-check.XXXXXX)}"
TARGET_DB="certos_restore_check_$$"

cleanup() {
  dropdb --if-exists "$TARGET_DB" >/dev/null 2>&1 || true
  [ -z "$KEEP_DUMP" ] && rm -f "$DUMP"
}
trap cleanup EXIT

q() { psql -X -At -v ON_ERROR_STOP=1 -d "$1" -c "$2"; }

fingerprint() {
  local db="$1"
  q "$db" "SELECT 'rls', c.relname, c.relrowsecurity, c.relforcerowsecurity FROM pg_class c JOIN pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = 'public' AND c.relkind = 'r' ORDER BY 2"
  q "$db" "SELECT 'policy', tablename, policyname, cmd FROM pg_policies WHERE schemaname = 'public' ORDER BY 2, 3"
  q "$db" "SELECT 'trigger', event_object_table, trigger_name, event_manipulation FROM information_schema.triggers WHERE trigger_schema = 'public' ORDER BY 2, 3, 4"
  q "$db" "SELECT 'grant', table_name, grantee, privilege_type FROM information_schema.role_table_grants WHERE table_schema = 'public' AND grantee NOT IN ('postgres') ORDER BY 2, 3, 4"
  q "$db" "SELECT 'index', tablename, indexname FROM pg_indexes WHERE schemaname = 'public' ORDER BY 2, 3"
  # exact row count and a checksum of the full content of every table (rows are compared as text, order-independent)
  q "$db" "SELECT format('SELECT ''rows'', %L, count(*), coalesce(md5(string_agg(t::text, E''\\x01'' ORDER BY t::text)), '''') FROM %I t', tablename, tablename) FROM pg_tables WHERE schemaname = 'public' ORDER BY tablename" |
    while read -r stmt; do q "$db" "$stmt"; done
}

echo "1/5 dumping $SOURCE_DB"
pg_dump --format=custom --no-owner --dbname="$SOURCE_DB" --file="$DUMP"
echo "    dump size: $(wc -c <"$DUMP") bytes"

echo "2/5 restoring into $TARGET_DB"
createdb "$TARGET_DB"
pg_restore --no-owner --exit-on-error --dbname="$TARGET_DB" "$DUMP"

echo "3/5 comparing structure and data"
A="$(mktemp)"; B="$(mktemp)"
fingerprint "$SOURCE_DB" >"$A"
fingerprint "$TARGET_DB" >"$B"
if ! diff -u "$A" "$B"; then
  echo "FAIL: the restored database differs from the source (see diff above)" >&2
  rm -f "$A" "$B"
  exit 1
fi
TABLES="$(grep -c '^rows|' "$A" || true)"
ROWS="$(awk -F'|' '/^rows\|/ {s += $3} END {print s+0}' "$A")"
rm -f "$A" "$B"
echo "    $TABLES tables, $ROWS rows, identical content checksums, RLS flags, policies, triggers, indexes and grants"

echo "4/5 checking that row-level security still fails closed in the restored copy"
if q "$TARGET_DB" "SELECT 1 FROM pg_roles WHERE rolname = 'certos_runtime'" | grep -q 1; then
  VISIBLE="$(psql -X -At -v ON_ERROR_STOP=1 -d "$TARGET_DB" -c "SET ROLE certos_runtime; SELECT count(*) FROM certificates" | tail -1)"
  if [ "$VISIBLE" != "0" ]; then
    echo "FAIL: the restricted role sees $VISIBLE certificate rows without a tenant" >&2
    exit 1
  fi
  echo "    restricted role sees 0 tenant rows without a tenant context"
else
  echo "    SKIPPED: role certos_runtime does not exist on this server (create it before a real restore)"
fi

echo "5/5 cleaning up"
echo "RESTORE VERIFIED: $SOURCE_DB -> dump -> $TARGET_DB (dropped)"
