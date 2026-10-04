#!/usr/bin/env bash
# build.sh — produce a release binary for the current platform.
#
# Self-healing: if the toolchain or the frontend dependencies are missing or out
# of date, it runs ./prepare.sh first. A fresh clone therefore needs only:
#
#     ./build.sh
#
# Usage:
#   ./build.sh                  # production build (what install.sh consumes)
#   ./build.sh --debug          # debug build: devtools, verbose logging
#   ./build.sh --race           # build with -race (Go tests + slow runtime)
#   ./build.sh --clean          # discard build outputs first
#   ./build.sh --no-prepare     # fail instead of auto-running prepare.sh
#   ./build.sh --skip-tests     # build only, no Go test suite
#   ./build.sh --output DIR     # also copy the binary into DIR
#
# Environment:
#   WAILS_VERSION   override the pinned Wails CLI version
#   LDFLAGS_EXTRA   extra -ldflags, appended after the project's own
#
# Output:
#   build/bin/hsx2mail            (Linux/BSD)
#   build/bin/hsx2mail.exe        (Windows)
#   hsx2mail.app                  (macOS, bundle)

set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

# Shared toolchain resolution: which `go` wins, and how the wails CLI is
# versioned. build.sh must agree with prepare.sh exactly, otherwise the build
# runs against a different compiler than the one that verified the tree.
# shellcheck source=scripts/toolchain.sh
. "$REPO_ROOT/scripts/toolchain.sh"

# ── output helpers ───────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
	C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'
	C_BLU=$'\033[34m'; C_BLD=$'\033[1m'; C_DIM=$'\033[2m'; C_OFF=$'\033[0m'
else
	C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''; C_DIM=''; C_OFF=''
fi

step() { printf '%s==>%s %s\n' "$C_BLU$C_BLD" "$C_OFF" "$*"; }
info() { printf '    %s\n' "$*"; }
ok()   { printf '    %s✓%s %s\n' "$C_GRN" "$C_OFF" "$*"; }
warn() { printf '    %s!%s %s\n' "$C_YEL" "$C_OFF" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_RED$C_BLD" "$C_OFF" "$*" >&2; exit 1; }

# ── options ──────────────────────────────────────────────────────────────────

BUILD_MODE="production"   # production | debug
WITH_RACE=0
CLEAN=0
RUN_PREPARE=1
RUN_TESTS=1
OUTPUT_DIR=""

for arg in "$@"; do
	case "$arg" in
		--debug)      BUILD_MODE="debug" ;;
		--race)       WITH_RACE=1 ;;
		--clean)      CLEAN=1 ;;
		--no-prepare) RUN_PREPARE=0 ;;
		--skip-tests) RUN_TESTS=0 ;;
		--output)     shift; OUTPUT_DIR="${1:-}" ;;
		--output=*)   OUTPUT_DIR="${arg#*=}" ;;
		-h|--help)    sed -n '2,28p' "$0"; exit 0 ;;
		*) die "unknown option '$arg' (try --help)" ;;
	esac
done

# OAuth client ids come from .env / .env.local, the same files the Makefile
# reads. Parsed here rather than `source`d: these files may hold values with
# spaces or `#`, and sourcing arbitrary shell from a config file is avoidable.
load_dotenv() {
	local file line key value
	for file in .env .env.local; do
		[[ -f "$file" ]] || continue
		while IFS= read -r line || [[ -n "$line" ]]; do
			line="${line%%$'\r'}"
			# skip blanks and comments
			[[ -z "${line//[[:space:]]/}" ]] && continue
			[[ "$line" =~ ^[[:space:]]*# ]] && continue
			[[ "$line" != *=* ]] && continue
			key="${line%%=*}"
			value="${line#*=}"
			# trim surrounding whitespace, then one layer of quotes
			key="$(printf '%s' "$key" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
			value="$(printf '%s' "$value" | sed 's/^[[:space:]]*//; s/[[:space:]]*$//')"
			if [[ "$value" == \"*\" && "$value" == *\" ]]; then
				value="${value:1:${#value}-2}"
			elif [[ "$value" == \'*\' && "$value" == *\' ]]; then
				value="${value:1:${#value}-2}"
			fi
			[[ "$key" =~ ^[A-Za-z_][A-Za-z0-9_]*$ ]] || continue
			# .env.local wins, so only fill what is still empty
			[[ -n "${!key:-}" ]] && continue
			printf -v "$key" '%s' "$value"
			export "${key?}"
		done <"$file"
	done
}
load_dotenv

UNAME_S="$(uname -s)"
case "$UNAME_S" in
	Linux)  PLATFORM="linux" ;;
	Darwin) PLATFORM="darwin" ;;
	*)      die "unsupported platform for wails build: $UNAME_S" ;;
esac

# The project pins WebKit2GTK 4.1 in its Makefile; keep them identical so a
# build here matches what CI and the Flatpak manifest produce.
BUILD_TAGS="desktop,webkit2_41"
[[ "$BUILD_MODE" == "debug" ]] && BUILD_TAGS="$BUILD_TAGS,debug"

WAILS_VERSION="${WAILS_VERSION:-$(awk '/github.com\/wailsapp\/wails\/v2 v/ {print $2; exit}' go.mod || true)}"
WAILS_VERSION="${WAILS_VERSION:-v2.12.0}"

# ── preflight: everything the build needs ─────────────────────────────────────

# Why a rebuild is needed, one line per reason. Empty output means "ready".
preflight_problems() {
	local problems=()

	if ! command -v go >/dev/null 2>&1; then
		problems+=("Go is not on PATH")
	else
		local want got
		want="$(awk '/^go /{print $2; exit}' go.mod)"
		got="$(go version | awk '{print $3}' | sed 's/^go//')"
		local lowest
		lowest="$(printf '%s\n%s\n' "$want" "$got" | sort -V | head -n1)"
		[[ "$lowest" == "$got" && "$lowest" != "$want" ]] && problems+=("Go $got is older than the $want required by go.mod")
	fi

	if ! command -v npm >/dev/null 2>&1; then
		problems+=("npm is not on PATH")
	fi

	local wv
	wv="$(wails_version || true)"
	if [[ -z "$wv" ]]; then
		problems+=("the wails CLI is not installed")
	elif [[ "$wv" != "$WAILS_VERSION" ]]; then
		problems+=("wails $wv is installed but go.mod pins $WAILS_VERSION")
	fi

	if [[ "$PLATFORM" == "linux" ]] && command -v pkg-config >/dev/null 2>&1; then
		pkg-config --exists gtk+-3.0 2>/dev/null ||
			problems+=("GTK+ 3 development files are missing")
		pkg-config --exists webkit2gtk-4.1 2>/dev/null ||
			problems+=("WebKit2GTK 4.1 development files are missing")
	fi

	if [[ ! -d frontend/node_modules ]]; then
		problems+=("frontend/node_modules is missing")
	fi

	printf '%s\n' "${problems[@]+"${problems[@]}"}"
}

needs_prepare() {
	# A stamp only tells us prepare ran at least once; the checks above tell us
	# whether the tree is currently usable.
	[[ -n "$(preflight_problems)" ]]
}

run_prepare() {
	if ((RUN_PREPARE == 0)); then
		die "environment is not ready and --no-prepare was given:
$(preflight_problems | sed 's/^/      - /')"
	fi
	step "Environment not ready — running ./prepare.sh"
	(cd "$REPO_ROOT" && ./prepare.sh) ||
		die "prepare.sh failed"
}

preflight() {
	step "Preflight"

	# Pick the toolchain before checking it, so a machine with several Go
	# installations resolves the same way prepare.sh did. select_go also clears a
	# stale GOROOT, which otherwise points the chosen `go` at another tree's
	# compiler.
	if ! select_go >/dev/null; then
		die "no Go toolchain found (need $(go_version_required) or newer)"
	fi
	local gv="$GO_SELECTED_VERSION"
	if [[ "$GO_SELECTED_EXACT" != "yes" ]]; then
		warn "using Go $gv, but go.mod pins $(go_version_required)"
		warn "if the build fails with 'package ... without types was imported',"
		warn "run ./prepare.sh to install the pinned toolchain"
	fi
	local problems
	problems="$(preflight_problems)"
	if [[ -n "$problems" ]]; then
		info "not ready:"
		printf '%s\n' "$problems" | sed 's/^/      - /'
		run_prepare
	fi

	# prepare.sh may have installed the CLI into ~/.local/bin.
	[[ -x "$HOME/.local/bin/wails" ]] && export PATH="$HOME/.local/bin:$PATH"

	local problems2
	problems2="$(preflight_problems)"
	[[ -z "$problems2" ]] || die "still not ready after prepare.sh:
$(printf '%s\n' "$problems2" | sed 's/^/      - /')"

	ok "go $(go version | awk '{print $3}'), node $(node --version), wails $(wails_version)"
}

# ── steps ────────────────────────────────────────────────────────────────────

do_clean() {
	step "Cleaning"
	rm -rf build/bin frontend/dist
	mkdir -p build/bin
	ok "removed build/bin and frontend/dist"
}

# The OAuth client ids are compile-time ldflags in the Makefile. Warn loudly
# rather than fail: a build without them still runs, Gmail/Outlook just cannot
# authenticate.
check_oauth_env() {
	if [[ -f .env || -f .env.local ]]; then
		ok "OAuth credentials found in .env/.env.local"
	else
		warn "no .env/.env.local — Gmail/Outlook OAuth will not work in this build"
		warn "see .env.example"
	fi
}

run_go_tests() {
	if ((RUN_TESTS == 0)); then
		info "skipping Go tests (--skip-tests)"
		return 0
	fi
	step "Go tests"
	if ((WITH_RACE)); then
		info "with -race; this is slow"
		go test -race -count=1 ./... >/dev/null || die "go test -race failed"
	else
		go test -count=1 ./... >/dev/null || die "go test failed"
	fi
	ok "go test ./..."
}

# The static checks that do not need a browser. Kept in the build path because
# they are the ones that catch a broken tree before it reaches the user.
run_static_checks() {
	step "Static checks"

	if [[ -d tools/db/txcheck ]]; then
		if go run ./tools/db/txcheck >/dev/null 2>&1; then
			ok "no read-only transactions"
		else
			go run ./tools/db/txcheck || die "txcheck failed"
		fi
	fi

	if [[ -f frontend/scripts/check-offline-icons.mjs ]]; then
		(cd frontend && node scripts/check-offline-icons.mjs >/dev/null) ||
			die "offline icon check failed"
		ok "offline icon coverage"
	fi
}

do_build() {
	step "Building ($BUILD_MODE)"
	local extra=()
	((WITH_RACE)) && extra+=("-race")

	# -clean forces wails to redo the frontend build instead of trusting a stale
	# frontend/dist, which is the usual cause of "installed binary shows the old
	# UI".
	local log
	log="$(mktemp)"
	local rc=0
	if [[ "$BUILD_MODE" == "debug" ]]; then
		wails build -debug -clean -tags "$BUILD_TAGS" "${extra[@]+"${extra[@]}"}" 2>&1 | tee "$log" || rc=$?
	else
		wails build -clean -tags "$BUILD_TAGS" "${extra[@]+"${extra[@]}"}" 2>&1 | tee "$log" || rc=$?
	fi
	((rc == 0)) && { rm -f "$log"; return 0; }

	# The Wails CLI reads package export data through golang.org/x/tools. A `go`
	# newer than the CLI was built against cannot be read, and the error names a
	# different package every run, so it reads like an unrelated compile error.
	if grep -q "without types was imported" "$log"; then
		rm -f "$log"
		die "wails $WAILS_VERSION cannot read the export data of Go $(go version | awk '{print $3}').
  The project pins $(go_version_required) (go.mod, CI, Flatpak all agree).
  Fix:  ./prepare.sh    # installs the pinned Go and clears the mixed build cache"
	fi
	if grep -q "does not match go tool version" "$log"; then
		rm -f "$log"
		die "the Go build cache holds entries from a different toolchain.
  Fix:  ./prepare.sh    # detects the switch and runs 'go clean -cache'"
	fi
	rm -f "$log"
	die "wails build failed (full log above)"
}

# The OAuth credential helper is a second binary that the app looks for next to
# itself (internal/oauth2/config.go: filepath.Dir(exe)/hsx2mail-creds) and under
# /app/lib/hsx2mail for Flatpak. Without it the app still starts but cannot read
# OAuth credentials, so it is part of the deliverable, not an extra.
build_creds_helper() {
	local out="build/bin/hsx2mail-creds"
	[[ -f cmd/hsx2mail-creds/main.go ]] || return 0

	local ldflags=""
	local var value
	for var in GoogleClientID GoogleClientSecret MicrosoftClientID \
	           GoogleTestingClientID GoogleTestingClientSecret; do
		case "$var" in
			GoogleClientID)          value="${GOOGLE_CLIENT_ID:-}" ;;
			GoogleClientSecret)      value="${GOOGLE_CLIENT_SECRET:-}" ;;
			MicrosoftClientID)        value="${MICROSOFT_CLIENT_ID:-}" ;;
			GoogleTestingClientID)    value="${GOOGLE_TESTING_CLIENT_ID:-}" ;;
			GoogleTestingClientSecret) value="${GOOGLE_TESTING_CLIENT_SECRET:-}" ;;
		esac
		# ldflags -X is a no-op on an empty value, so only pass what is set.
		[[ -n "$value" ]] || continue
		# The helper declares these in package main, not in internal/oauth2, so
		# -X must name main.<Var>. A wrong import path is silently ignored by
		# the linker and produces a helper that reports empty credentials.
		ldflags+="${ldflags:+ }-X 'main.${var}=${value}'"
	done

	info "building hsx2mail-creds"
	if [[ -n "$ldflags" ]]; then
		go build -o "$out" -ldflags "$ldflags" ./cmd/hsx2mail-creds ||
			die "failed to build the OAuth credential helper"
	else
		go build -o "$out" ./cmd/hsx2mail-creds ||
			die "failed to build the OAuth credential helper"
	fi
	ok "built $out"
}

locate_artifact() {
	local bin
	if [[ "$PLATFORM" == "darwin" ]]; then
		bin="build/bin/hsx2mail.app/Contents/MacOS/hsx2mail"
	else
		bin="build/bin/hsx2mail"
	fi
	[[ -f "$bin" ]] || bin="build/bin/hsx2mail.exe"
	[[ -f "$bin" ]] || die "build reported success but no binary was found under build/bin"
	printf '%s\n' "$bin"
}

verify_artifact() {
	local bin="$1"
	[[ -x "$bin" ]] || die "built binary is not executable: $bin"

	# A binary that cannot resolve GTK/WebKit is the classic way a Linux build
	# "works" until it is launched. Link-time checking catches it here.
	local linked_ok=1
	if command -v ldd >/dev/null 2>&1; then
		local missing
		# `grep` exits 1 when nothing matches, which under `set -e -o pipefail`
		# would abort the whole script from inside a command substitution.
		missing="$(ldd "$bin" 2>/dev/null | grep 'not found' | awk '{print $1}' | tr '\n' ' ' || true)"
		if [[ -n "${missing// /}" ]]; then
			warn "unresolved shared libraries: $missing"
			linked_ok=0
		fi
	fi

	local size
	size="$(du -h "$bin" | cut -f1)"
	info "size: $size"

	# --version must not need a display; a crash here means the GUI bootstrap
	# runs before flag parsing.
	if command -v timeout >/dev/null 2>&1; then
		if timeout 10 "$bin" --help >/dev/null 2>&1; then
			ok "binary runs and responds to --help"
		elif [[ $linked_ok -eq 1 ]]; then
			warn "binary did not answer --help within 10s (a GUI app may need a display)"
		fi
	fi
	return 0
}

copy_output() {
	local bin="$1"
	[[ -n "$OUTPUT_DIR" ]] || return 0
	mkdir -p "$OUTPUT_DIR"
	cp -f "$bin" "$OUTPUT_DIR/"
	ok "copied to $OUTPUT_DIR/$(basename "$bin")"
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
	printf '%s%sHsx2Mail — build%s  (%s%s%s, tags: %s)\n\n' \
		"$C_BLD" "$C_BLU" "$C_OFF" "$C_DIM" "$BUILD_MODE/$PLATFORM" "$C_OFF" "$BUILD_TAGS"

	((CLEAN)) && do_clean
	preflight
	check_oauth_env
	run_static_checks
	run_go_tests
	do_build

	build_creds_helper

	local bin
	bin="$(locate_artifact)"
	step "Verifying artifact"
	verify_artifact "$bin"
	copy_output "$bin"

	printf '\n%s%sBuild complete.%s\n' "$C_BLD" "$C_GRN" "$C_OFF"
	case "$PLATFORM" in
		linux)  printf '   Binary: %s\n' "$REPO_ROOT/$bin" ;;
		darwin) printf '   Bundle: %s\n' "$REPO_ROOT/build/bin/hsx2mail.app" ;;
	esac
	printf '   Install: ./install.sh\n\n'
}

main "$@"
