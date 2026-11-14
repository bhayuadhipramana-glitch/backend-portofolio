#!/bin/sh
# Offline test for backup_postgres_azure.sh. pg_dump, pg_restore, curl and az are
# replaced by stubs; openssl is real, so encryption is genuinely exercised.
#   sh deploy/scripts/backup_postgres_azure_test.sh
set -eu

HERE=$(cd "$(dirname "$0")" && pwd)
SCRIPT="$HERE/backup_postgres_azure.sh"
ROOT=$(mktemp -d)
trap 'rm -rf "$ROOT"' EXIT
FAILS=0

fail() { printf 'FAIL: %s\n' "$*" >&2; FAILS=$((FAILS + 1)); }
pass() { printf 'ok:   %s\n' "$*"; }

# make_stubs DIR: pg_dump prints FAKEDUMP (or fails when STUB_PGDUMP_FAIL=1);
# pg_restore --list succeeds only for FAKEDUMP on stdin; curl and az log their
# calls and serve a canned blob listing.
make_stubs() {
	d=$1
	mkdir -p "$d"
	cat >"$d/pg_dump" <<'STUB'
#!/bin/sh
[ "${STUB_PGDUMP_FAIL:-0}" = 1 ] && { echo "connection refused" >&2; exit 1; }
printf 'FAKEDUMP'
STUB
	cat >"$d/pg_restore" <<'STUB'
#!/bin/sh
[ "$(cat)" = FAKEDUMP ]
STUB
	cat >"$d/curl" <<'STUB'
#!/bin/sh
method=GET; file=""; hdrs=""
while [ $# -gt 1 ]; do
	case $1 in
	--request) method=$2; shift ;;
	--upload-file) file=$2; shift ;;
	--header) hdrs="$hdrs|$2"; shift ;;
	esac
	shift
done
url=$1
echo "curl $method $url$hdrs" >>"$STUB_LOG"
[ "$method" = PUT ] && cp "$file" "$STUB_OUT/uploaded"
[ "$method" = GET ] && printf '%s' "$STUB_LISTING_XML"
exit 0
STUB
	cat >"$d/az" <<'STUB'
#!/bin/sh
echo "az $*" >>"$STUB_LOG"
case "$*" in
*"blob upload"*) while [ $# -gt 0 ]; do [ "$1" = --file ] && cp "$2" "$STUB_OUT/uploaded"; shift; done ;;
*"blob list"*) printf '%s\n' $STUB_LISTING_NAMES ;;
esac
exit 0
STUB
	chmod +x "$d"/*
}

# run_case NAME STUBDIR: runs the script, sets RC and OUT.
run_case() {
	STUB_LOG="$ROOT/$1.log"
	STUB_OUT="$ROOT/$1.out"
	mkdir -p "$STUB_OUT"
	: >"$STUB_LOG"
	export STUB_LOG STUB_OUT
	RC=0
	OUT=$(PATH="$2:$PATH" sh "$SCRIPT" 2>&1) || RC=$?
	LOG="$STUB_LOG"
	UPLOADED="$STUB_OUT/uploaded"
}

export DATABASE_URL="postgres://u:p@db/x"
export BACKUP_ENCRYPTION_KEY="test-key-0123456789"
export AZURE_STORAGE_ACCOUNT="acct"
export AZURE_STORAGE_CONTAINER="pgbackups"
export AZURE_STORAGE_SAS_TOKEN="?sv=2022&sig=abc"
export BACKUP_TMPDIR="$ROOT"

RECENT=portfolio-pg-$(date -u +%Y%m%dT%H%M%SZ).dump.enc
OLD=portfolio-pg-20200101T000000Z.dump.enc
export STUB_LISTING_XML="<EnumerationResults><Blobs><Blob><Name>$OLD</Name></Blob><Blob><Name>$RECENT</Name></Blob><Blob><Name>unrelated.txt</Name></Blob><Blob><Name>portfolio-pg-notatimestamp.dump.enc</Name></Blob></Blobs></EnumerationResults>"
export STUB_LISTING_NAMES="$OLD $RECENT unrelated.txt"

# --- 1. missing configuration -> exit 2, nothing runs ---------------------------
make_stubs "$ROOT/curlbin"
RC=0
OUT=$(env -u BACKUP_ENCRYPTION_KEY PATH="$ROOT/curlbin:$PATH" sh "$SCRIPT" 2>&1) || RC=$?
[ "$RC" = 2 ] && printf '%s' "$OUT" | grep -q BACKUP_ENCRYPTION_KEY && pass "missing key is a config error" || fail "missing key: rc=$RC out=$OUT"

# --- 2. curl backend: encrypt, upload to Cool, prune only the old backup --------
# Hide az so the curl path is chosen. PATH keeps system tools, so shadow az with a failing stub.
mkdir -p "$ROOT/noaz"
cp "$ROOT/curlbin/"* "$ROOT/noaz/"
rm -f "$ROOT/noaz/az"
run_case curl "$ROOT/noaz"
[ "$RC" = 0 ] && pass "curl backend exits 0" || fail "curl backend rc=$RC out=$OUT"
grep -q "curl PUT https://acct.blob.core.windows.net/pgbackups/portfolio-pg-.*\.dump\.enc?sv=2022&sig=abc" "$LOG" && pass "PUT goes to the container with the SAS (leading ? stripped)" || fail "no PUT: $(cat "$LOG")"
grep -q "x-ms-access-tier: Cool" "$LOG" && pass "upload requests the Cool tier" || fail "Cool tier header missing"
grep -q "If-None-Match: \*" "$LOG" && pass "upload never overwrites an existing blob" || fail "If-None-Match missing"
grep -q "curl DELETE .*/$OLD?" "$LOG" && pass "backup older than 30 days is deleted" || fail "old backup not deleted: $(cat "$LOG")"
[ "$(grep -c 'curl DELETE' "$LOG")" = 1 ] && pass "recent, unrelated and malformed names are kept" || fail "unexpected deletes: $(grep 'curl DELETE' "$LOG")"
if [ -s "$UPLOADED" ]; then
	grep -q FAKEDUMP "$UPLOADED" && fail "uploaded file contains plaintext" || pass "uploaded file is not plaintext"
	dec=$(openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -pass env:BACKUP_ENCRYPTION_KEY -in "$UPLOADED")
	[ "$dec" = FAKEDUMP ] && pass "uploaded file decrypts with the key" || fail "decrypted content = $dec"
	head -c 8 "$UPLOADED" | grep -q '^Salted__' && pass "salted openssl format" || fail "no Salted__ header"
else
	fail "nothing was uploaded"
fi
[ -z "$(ls "$ROOT"/pgbackup.* 2>/dev/null)" ] && pass "scratch directory removed" || fail "scratch directory left behind"
printf '%s' "$OUT" | grep -q "p@\|u:p" && fail "log leaks the database password" || pass "log does not leak credentials"

# --- 3. pg_dump failure -> exit 1, no upload, no prune --------------------------
STUB_PGDUMP_FAIL=1 run_case dumpfail "$ROOT/noaz"
[ "$RC" = 1 ] && pass "pg_dump failure fails the run" || fail "pg_dump failure rc=$RC"
! grep -q "PUT\|DELETE" "$LOG" && pass "a failed dump uploads and deletes nothing" || fail "calls after failed dump: $(cat "$LOG")"

# --- 4. wrong key can not read the archive (sanity of the format) ---------------
if [ -s "$ROOT/curl.out/uploaded" ]; then
	if BACKUP_ENCRYPTION_KEY=wrong openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -pass env:BACKUP_ENCRYPTION_KEY -in "$ROOT/curl.out/uploaded" 2>/dev/null | grep -q FAKEDUMP; then
		fail "wrong key decrypted the archive"
	else
		pass "wrong key does not decrypt"
	fi
fi

# --- 5. Azure CLI backend uses --tier Cool --------------------------------------
run_case az "$ROOT/curlbin"
[ "$RC" = 0 ] && pass "az backend exits 0" || fail "az backend rc=$RC out=$OUT"
grep -q "blob upload .*--tier Cool" "$LOG" && pass "az upload uses --tier Cool" || fail "az upload: $(cat "$LOG")"
grep -q "blob delete .*--name $OLD" "$LOG" && pass "az prunes the old backup" || fail "az delete: $(cat "$LOG")"
[ "$(grep -c 'blob delete' "$LOG")" = 1 ] && pass "az keeps recent backups" || fail "az deleted too much"

# --- 6. retention override ------------------------------------------------------
BACKUP_RETENTION_DAYS=0 run_case badret "$ROOT/noaz"
[ "$RC" = 2 ] && pass "retention of 0 is rejected" || fail "retention 0 rc=$RC"

if [ "$FAILS" -gt 0 ]; then
	printf '\n%d check(s) failed\n' "$FAILS" >&2
	exit 1
fi
printf '\nall checks passed\n'
