#!/bin/sh
# Encrypted PostgreSQL 17 backup to Azure Blob Storage (Cool tier) with pruning.
#
#   pg_dump -Fc  ->  openssl enc -aes-256-cbc -pbkdf2  ->  Azure Blob (Cool)  ->  delete > N days
#
# The plaintext dump is never written to disk: it is piped straight into openssl.
# POSIX sh only (runs on alpine/busybox). Run it from the k3s CronJob
# (deploy/k8s/backup-cronjob.yaml) or by hand.
#
# Required environment (never commit values; see .env.example):
#   DATABASE_URL              postgres://user:pass@host:5432/db
#   BACKUP_ENCRYPTION_KEY     passphrase for openssl (openssl rand -hex 32). Losing it
#                             makes every backup unreadable: keep a copy outside the cluster.
#   AZURE_STORAGE_ACCOUNT     storage account name
#   AZURE_STORAGE_CONTAINER   container name (default: pgbackups)
#   AZURE_STORAGE_SAS_TOKEN   container-scoped SAS with create/write/list/delete (no leading "?")
# Optional:
#   BACKUP_RETENTION_DAYS     delete backups older than this (default 30; Cool tier bills
#                             30 days minimum anyway, so keeping fewer saves nothing)
#   BACKUP_PREFIX             blob name prefix (default: portfolio-pg-)
#   BACKUP_TMPDIR             scratch dir for the encrypted file (default: /tmp)
#
# Upload uses the Azure CLI (`az ... --tier Cool`) when installed, otherwise plain
# curl against the Blob REST API (x-ms-access-tier: Cool). Both authenticate with the SAS.
#
# Restore:
#   openssl enc -d -aes-256-cbc -pbkdf2 -iter 600000 -pass env:BACKUP_ENCRYPTION_KEY \
#       -in portfolio-pg-<timestamp>.dump.enc | pg_restore --clean --if-exists -d "$DATABASE_URL"
#
# Exit codes: 0 ok, 1 failure, 2 bad configuration.

set -eu

KDF_ITER=600000
API_VERSION=2022-11-02

log() { printf '%s backup: %s\n' "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$*"; }
die() { log "ERROR: $*" >&2; exit "${2:-1}"; }

require() {
	eval "_v=\${$1:-}"
	[ -n "$_v" ] || die "environment variable $1 is not set" 2
}

require DATABASE_URL
require BACKUP_ENCRYPTION_KEY
require AZURE_STORAGE_ACCOUNT
require AZURE_STORAGE_SAS_TOKEN
CONTAINER=${AZURE_STORAGE_CONTAINER:-pgbackups}
RETENTION_DAYS=${BACKUP_RETENTION_DAYS:-30}
PREFIX=${BACKUP_PREFIX:-portfolio-pg-}
TMPROOT=${BACKUP_TMPDIR:-/tmp}

case $RETENTION_DAYS in
'' | *[!0-9]*) die "BACKUP_RETENTION_DAYS must be a positive integer" 2 ;;
esac
[ "$RETENTION_DAYS" -ge 1 ] || die "BACKUP_RETENTION_DAYS must be at least 1" 2
case $CONTAINER$PREFIX in
*[!A-Za-z0-9._-]*) die "container and prefix may only contain letters, digits, '.', '_' and '-'" 2 ;;
esac

SAS=${AZURE_STORAGE_SAS_TOKEN#\?}
BASE_URL="https://${AZURE_STORAGE_ACCOUNT}.blob.core.windows.net/${CONTAINER}"

if command -v az >/dev/null 2>&1; then
	TOOL=az
elif command -v curl >/dev/null 2>&1; then
	TOOL=curl
else
	die "neither the Azure CLI (az) nor curl is installed" 2
fi
command -v pg_dump >/dev/null 2>&1 || die "pg_dump is not installed" 2
command -v pg_restore >/dev/null 2>&1 || die "pg_restore is not installed" 2
command -v openssl >/dev/null 2>&1 || die "openssl is not installed" 2

# --- Azure helpers: one implementation per tool ---------------------------------

# upload FILE BLOBNAME: create a new blob in the Cool tier; never overwrite.
upload() {
	case $TOOL in
	az)
		az storage blob upload --only-show-errors \
			--account-name "$AZURE_STORAGE_ACCOUNT" --container-name "$CONTAINER" \
			--sas-token "$SAS" --name "$2" --file "$1" --tier Cool >/dev/null
		;;
	curl)
		curl --fail --silent --show-error --max-time 600 --request PUT \
			--header "x-ms-version: $API_VERSION" \
			--header "x-ms-blob-type: BlockBlob" \
			--header "x-ms-access-tier: Cool" \
			--header "If-None-Match: *" \
			--header "Content-Type: application/octet-stream" \
			--upload-file "$1" "$BASE_URL/$2?$SAS" >/dev/null
		;;
	esac
}

# list_blobs: print the names of our backups, one per line.
list_blobs() {
	case $TOOL in
	az)
		az storage blob list --only-show-errors \
			--account-name "$AZURE_STORAGE_ACCOUNT" --container-name "$CONTAINER" \
			--sas-token "$SAS" --prefix "$PREFIX" --query '[].name' --output tsv
		;;
	curl)
		curl --fail --silent --show-error --max-time 60 \
			--header "x-ms-version: $API_VERSION" \
			"$BASE_URL?restype=container&comp=list&prefix=$PREFIX&$SAS" |
			tr '<' '\n' | sed -n 's/^Name>//p'
		;;
	esac
}

# delete_blob BLOBNAME
delete_blob() {
	case $TOOL in
	az)
		az storage blob delete --only-show-errors \
			--account-name "$AZURE_STORAGE_ACCOUNT" --container-name "$CONTAINER" \
			--sas-token "$SAS" --name "$1" >/dev/null
		;;
	curl)
		curl --fail --silent --show-error --max-time 60 --request DELETE \
			--header "x-ms-version: $API_VERSION" "$BASE_URL/$1?$SAS" >/dev/null
		;;
	esac
}

# --- 1. dump + encrypt (no plaintext on disk) -----------------------------------

STAMP=$(date -u +%Y%m%dT%H%M%SZ)
BLOB="${PREFIX}${STAMP}.dump.enc"
WORKDIR=$(mktemp -d "$TMPROOT/pgbackup.XXXXXX")
trap 'rm -rf "$WORKDIR"' EXIT INT TERM HUP
umask 077
ENC="$WORKDIR/$BLOB"
STATUS="$WORKDIR/pg_dump.status"

log "dumping database to $BLOB"
# POSIX sh has no pipefail, so pg_dump's exit status travels through a file.
{
	pg_dump --format=custom --no-owner --no-privileges "$DATABASE_URL" 2>"$WORKDIR/pg_dump.err"
	echo $? >"$STATUS"
} | openssl enc -aes-256-cbc -pbkdf2 -iter "$KDF_ITER" -salt \
	-pass env:BACKUP_ENCRYPTION_KEY -out "$ENC" || die "encryption failed"

DUMP_RC=$(cat "$STATUS" 2>/dev/null || echo 1)
if [ "$DUMP_RC" != "0" ]; then
	# pg_dump's own message may name the host; it never contains the password.
	sed 's/^/pg_dump: /' "$WORKDIR/pg_dump.err" >&2 || true
	die "pg_dump exited with status $DUMP_RC"
fi

# --- 2. verify the archive can be decrypted and read before uploading -----------

openssl enc -d -aes-256-cbc -pbkdf2 -iter "$KDF_ITER" -pass env:BACKUP_ENCRYPTION_KEY -in "$ENC" |
	pg_restore --list >/dev/null 2>&1 || die "verification failed: the encrypted archive cannot be decrypted and listed"
SIZE=$(wc -c <"$ENC" | tr -d ' ')
[ "$SIZE" -gt 0 ] || die "encrypted archive is empty"
log "archive verified ($SIZE bytes)"

# --- 3. upload ------------------------------------------------------------------

log "uploading to $CONTAINER with $TOOL (tier Cool)"
upload "$ENC" "$BLOB" || die "upload failed"
log "uploaded $BLOB"

# --- 4. prune old backups -------------------------------------------------------

# Names embed a UTC timestamp, so pruning compares names, not blob metadata.
# `date -d @EPOCH` works on GNU coreutils and busybox.
CUTOFF=$(date -u -d "@$(($(date +%s) - RETENTION_DAYS * 86400))" +%Y%m%dT%H%M%SZ)
log "pruning backups older than $RETENTION_DAYS days (before $CUTOFF)"
NAMES=$(list_blobs) || die "listing backups failed (the new backup is safe)"
PRUNED=0
for name in $NAMES; do
	case $name in
	"$PREFIX"*.dump.enc) ;;
	*) continue ;;
	esac
	ts=${name#"$PREFIX"}
	ts=${ts%.dump.enc}
	case $ts in
	[0-9][0-9][0-9][0-9][0-9][0-9][0-9][0-9]T[0-9][0-9][0-9][0-9][0-9][0-9]Z) ;;
	*) continue ;;
	esac
	# Fixed-width UTC timestamps sort lexically; `sort` avoids the non-POSIX `\<` test operator.
	if [ "$(printf '%s\n%s\n' "$ts" "$CUTOFF" | sort | head -n 1)" = "$ts" ] && [ "$ts" != "$CUTOFF" ]; then
		delete_blob "$name" || die "failed to delete $name"
		log "deleted $name"
		PRUNED=$((PRUNED + 1))
	fi
done
log "done: 1 uploaded, $PRUNED pruned"
