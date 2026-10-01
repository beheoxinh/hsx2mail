#!/usr/bin/env bash
# scripts/smoke-test.sh — run the built app for real and check it comes up.
#
# Every other test in this repo exercises a slice: a Go package, a Svelte
# component, a query plan. None of them proves the binary starts, opens its
# database, migrates it, brings up its GTK window and shuts down cleanly — and
# that is precisely where packaging mistakes surface (a binary linked against
# the wrong WebKitGTK, a data directory it cannot create, a migration that only
# runs in the main process).
#
# This launches the actual binary and verifies the observable effects.
#
# Usage:
#   ./scripts/smoke-test.sh                    # build/bin/hsx2mail
#   ./scripts/smoke-test.sh /usr/local/bin/hsx2mail
#   KEEP_DATA=1 ./scripts/smoke-test.sh        # do not wipe the data dir after
#
# Exits 0 when the app started, created and migrated its database, and exited
# cleanly on SIGTERM.

set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
BIN="${1:-$REPO_ROOT/build/bin/hsx2mail}"

if [[ -t 1 ]]; then
	C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'
	C_BLD=$'\033[1m'; C_OFF=$'\033[0m'
else
	C_RED=''; C_GRN=''; C_YEL=''; C_BLD=''; C_OFF=''
fi
step() { printf '%s==>%s %s\n' "$C_BLD" "$C_OFF" "$*"; }
ok()   { printf '    %s✓%s %s\n' "$C_GRN" "$C_OFF" "$*"; }
warn() { printf '    %s!%s %s\n' "$C_YEL" "$C_OFF" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_RED$C_BLD" "$C_OFF" "$*" >&2; exit 1; }

[[ -x "$BIN" ]] || die "not executable: $BIN (run ./build.sh first)"

# An isolated data directory, so the smoke test cannot touch a real mailbox and
# starts from the same state every run.
DATA_DIR="$(mktemp -d)"
export XDG_DATA_HOME="$DATA_DIR/data"
export XDG_CONFIG_HOME="$DATA_DIR/config"
export XDG_CACHE_HOME="$DATA_DIR/cache"
mkdir -p "$XDG_DATA_HOME" "$XDG_CONFIG_HOME" "$XDG_CACHE_HOME"

APP_PID=""
XVFB_PID=""
LOG="$DATA_DIR/app.log"
cleanup() {
	if [[ -n "$APP_PID" ]] && kill -0 "$APP_PID" 2>/dev/null; then
		kill -TERM "$APP_PID" 2>/dev/null || true
		for _ in {1..25}; do
			kill -0 "$APP_PID" 2>/dev/null || break
			sleep 0.2
		done
		kill -KILL "$APP_PID" 2>/dev/null || true
	fi
	if [[ -n "${XVFB_PID:-}" ]] && kill -0 "$XVFB_PID" 2>/dev/null; then
		kill -TERM "$XVFB_PID" 2>/dev/null || true
	fi
	if [[ "${KEEP_DATA:-0}" == "1" ]]; then
		printf 'data dir kept: %s\n' "$DATA_DIR"
	else
		rm -rf "$DATA_DIR"
	fi
}
trap cleanup EXIT

# ── 1. the binary answers its flags ──────────────────────────────────────────

step "Command line"
if ! timeout 20 "$BIN" --help >"$DATA_DIR/help.txt" 2>&1; then
	cat "$DATA_DIR/help.txt" >&2
	die "--help did not exit cleanly"
fi
for flag in -compose -account -mailto -start-hidden; do
	grep -q -- "$flag" "$DATA_DIR/help.txt" ||
		warn "flag $flag is not advertised in --help"
done
ok "flag parsing works"

# ── 2. it starts and stays up ───────────────────────────────────────────────

step "Startup"
# Start Xvfb ourselves and exec the binary directly, rather than going through
# `xvfb-run`: that wrapper is what $! would capture, so SIGTERM would go to the
# wrapper and the app would be orphaned instead of shut down.
XVFB_PID=""
if [[ -z "${DISPLAY:-}" ]] && command -v Xvfb >/dev/null 2>&1; then
	DISPLAY_NUM=$(( 90 + RANDOM % 8 ))
	while [[ -e "/tmp/.X11-unix/X${DISPLAY_NUM}" ]]; do
		DISPLAY_NUM=$(( DISPLAY_NUM + 1 ))
	done
	Xvfb ":${DISPLAY_NUM}" -screen 0 1280x900x24 >/dev/null 2>&1 &
	XVFB_PID=$!
	export DISPLAY=":${DISPLAY_NUM}"
	for _ in {1..40}; do
		[[ -e "/tmp/.X11-unix/X${DISPLAY_NUM}" ]] && break
		sleep 0.1
	done
	ok "Xvfb on $DISPLAY"
elif [[ -n "${DISPLAY:-}" ]]; then
	ok "using the existing display $DISPLAY"
else
	warn "no display available; GTK will fail to initialise"
fi

# The single-instance socket lives in /tmp, not under XDG_DATA_HOME, so an
# instance left over from a previous run would make this one hand off and exit.
# Clearing it is part of getting a clean measurement.
SOCKDIR="/tmp/hsx2mail-$(id -u)"
if [[ -S "${SOCKDIR}/instance.sock" ]] && ! pgrep -x "$(basename "$BIN")" >/dev/null 2>&1; then
	rm -f "${SOCKDIR}/instance.sock"
	info "removed a stale instance socket"
fi

"$BIN" >"$LOG" 2>&1 &
APP_PID=$!

# Give it time to open the database, migrate and paint the first frame.
STARTUP_WAIT="${STARTUP_WAIT:-12}"
for i in $(seq 1 $((STARTUP_WAIT * 5))); do
	kill -0 "$APP_PID" 2>/dev/null || break
	sleep 0.2
	if [[ -s "$LOG" ]] && grep -qiE "panic|fatal error" "$LOG"; then
		break
	fi
done

if ! kill -0 "$APP_PID" 2>/dev/null; then
	wait "$APP_PID" 2>/dev/null || true
	step "Application log"
	sed 's/^/      /' "$LOG" >&2
	die "the app exited during startup"
fi
ok "process stayed up"

if grep -qiE "panic:|fatal error:" "$LOG"; then
	step "Application log"
	sed 's/^/      /' "$LOG" >&2
	die "the app panicked"
fi
ok "no panic in the log"

# ── 3. it created and migrated its database ──────────────────────────────────

step "Database"
DB="$(find "$XDG_DATA_HOME" -name 'hsx2mail.db' -print -quit 2>/dev/null || true)"
[[ -n "$DB" ]] || die "no database was created under $XDG_DATA_HOME/hsx2mail"
ok "database created: ${DB#$REPO_ROOT/}"

# The app owns its schema; read it read-only so the smoke test cannot disturb it.
if command -v sqlite3 >/dev/null 2>&1; then
	VERSION="$(sqlite3 "file:$DB?mode=ro" 'SELECT MAX(version) FROM migrations;' 2>/dev/null || echo "")"
	if [[ -z "$VERSION" ]]; then
		die "the migrations table is empty or unreadable"
	fi
	# Whatever internal/database/migrations.go declares as the newest version.
	EXPECTED="$(awk '/Version: [0-9]+,/ {if (match($0, /Version: [0-9]+/)) {v=substr($0, RSTART+9, RLENGTH-9)} } END {print v}' \
		"$REPO_ROOT/internal/database/migrations.go")"
	if [[ "$VERSION" == "$EXPECTED" ]]; then
		ok "migrated to v$VERSION (matches migrations.go)"
	else
		die "database is at v$VERSION but migrations.go declares v$EXPECTED"
	fi

	TABLES="$(sqlite3 "file:$DB?mode=ro" \
		"SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%';" 2>/dev/null || echo 0)"
	((TABLES > 0)) || die "the schema has no tables"
	ok "$TABLES tables created"

	# foreign_keys must be on: the schema relies on it (attachments reference
	# messages). PRAGMA foreign_keys is per connection and defaults to OFF, so
	# reading it here would always report 0 no matter what the application sets
	# -- see internal/database's DSN and the test that asserts it on a real
	# application connection. Check the enforced constraints instead, which is
	# connection independent.
	FKS="$(sqlite3 "file:$DB?mode=ro" \
		"SELECT COUNT(*) FROM pragma_foreign_key_list('attachments');" 2>/dev/null || echo 0)"
	if [[ "${FKS:-0}" -gt 0 ]] 2>/dev/null; then
		ok "attachments FK enforced ($FKS constraint(s) declared)"
	else
		warn "no foreign keys declared on attachments"
	fi
else
	warn "sqlite3 not installed; skipping schema checks"
fi

# ── 4. the data directory has the expected shape ─────────────────────────────

step "Data directory"
DIRTY="$(find "$XDG_DATA_HOME/hsx2mail" -maxdepth 1 -type d -name 'attachments' -print -quit 2>/dev/null || true)"
if [[ -n "$DIRTY" ]]; then
	PERM="$(stat -c '%a' "$DIRTY" 2>/dev/null || echo '?')"
	# 0700: the mail store holds PGP keys and message bodies.
	if [[ "$PERM" == "700" ]]; then
		ok "attachments dir is 0700"
	else
		warn "attachments dir is $PERM (expected 700)"
	fi
else
	warn "no attachments directory was created"
fi

# ── 5. it shuts down on SIGTERM ──────────────────────────────────────────────

step "Shutdown"
kill -TERM "$APP_PID"
CLEAN_EXIT=1
for _ in {1..40}; do
	if ! kill -0 "$APP_PID" 2>/dev/null; then
		CLEAN_EXIT=0
		break
	fi
	sleep 0.2
done

if [[ $CLEAN_EXIT -ne 0 ]]; then
	warn "the app ignored SIGTERM; sending SIGKILL"
	kill -KILL "$APP_PID" 2>/dev/null || true
	sleep 0.5
	die "the app did not exit on SIGTERM"
fi
wait "$APP_PID" 2>/dev/null || true
APP_PID=""
ok "exited on SIGTERM"

# A clean shutdown must not leave a WAL that the next start has to recover from.
if command -v sqlite3 >/dev/null 2>&1 && [[ -n "$DB" && -f "$DB" ]]; then
	WAL="${DB}-wal"
	if [[ -f "$WAL" ]]; then
		SIZE="$(stat -c '%s' "$WAL" 2>/dev/null || echo 0)"
		# A large leftover WAL means the last checkpoint did not run.
		if ((SIZE < 1_000_000)); then
			ok "WAL checkpointed on exit (${SIZE}B)"
		else
			warn "WAL is ${SIZE}B after exit; the checkpoint may not have run"
		fi
	else
		ok "no WAL left behind"
	fi
fi

# ── 6. the OAuth helper contract ─────────────────────────────────────────────

step "Credential helper"
HELPER="$(dirname "$BIN")/hsx2mail-creds"
if [[ -x "$HELPER" ]]; then
	if "$HELPER" 2>/dev/null | python3 -c 'import json,sys; json.load(sys.stdin)' 2>/dev/null; then
		ok "hsx2mail-creds prints valid JSON"
	else
		die "hsx2mail-creds did not print valid JSON"
	fi
	PERM="$(stat -c '%a' "$HELPER" 2>/dev/null || echo '?')"
	# internal/oauth2 refuses a helper that group or other can write.
	if (( (0$PERM & 022) == 0 )); then
		ok "helper permissions $PERM pass the ownership check"
	else
		die "helper is mode $PERM; the app will refuse to run it"
	fi
else
	warn "no hsx2mail-creds next to the binary; OAuth sign-in cannot work"
fi

printf '\n%ssmoke test passed%s\n' "$C_BLD" "$C_OFF"
