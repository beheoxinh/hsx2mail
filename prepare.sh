#!/usr/bin/env bash
# prepare.sh — install everything needed to build and develop Email Hub
# (hsx2mail) on a freshly cloned checkout.
#
# Idempotent: safe to re-run. Each step checks first and only acts when
# something is missing or too old, so a second run costs a few seconds.
#
# What it installs
#   1. System build+runtime libraries (GTK3, WebKit2GTK 4.1, desktop-file-utils)
#   2. Go toolchain (>= the version in go.mod)
#   3. Node.js toolchain
#   4. The Wails v2 CLI (pinned to the module version in go.mod)
#   5. Frontend dependencies (npm ci)
#   6. Verification: the tree must build, type-check and pass its own gates
#
# Usage:
#   ./prepare.sh              # full prepare + verify
#   ./prepare.sh --no-verify  # install only, skip the verification pass
#   ./prepare.sh --check      # report what is missing, change nothing
#
# Environment overrides:
#   GO_VERSION       Go version to install if the system Go is too old (default 1.25.0)
#   NODE_MAJOR       Node major to install if the system Node is too old (default 22)
#   INSTALL_GO=0     never install Go, only check it
#   INSTALL_NODE=0   never install Node, only check it
#   PREFIX           install prefix for Go modules/bin (default /usr/local)
#
# Requires sudo for system packages. In CI (non-interactive) it uses `sudo -n`.

set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

# Shared toolchain resolution (Go selection, wails version, build-cache safety).
# shellcheck source=scripts/toolchain.sh
. "$REPO_ROOT/scripts/toolchain.sh"

# ── configuration ────────────────────────────────────────────────────────────

# Default to whatever go.mod declares, so this script and the compiler can
# never disagree about the target toolchain.
GO_VERSION="${GO_VERSION:-$(go_version_required)}"
GO_VERSION="${GO_VERSION:-1.25.0}"
NODE_MAJOR="${NODE_MAJOR:-22}"
PREFIX="${PREFIX:-/usr/local}"
WAILS_VERSION="$(awk '/github.com\/wailsapp\/wails\/v2 v/ {print $2; exit}' go.mod || true)"
WAILS_VERSION="${WAILS_VERSION:-v2.12.0}"

# Build tags for this project. WebKit2GTK 4.1 is the current API; the older
# 4.0 package is EOL and cannot compile the webkit2_41 code paths.
WAILS_BUILD_TAGS="desktop,webkit2_41,production"

DO_VERIFY=1
DRY_RUN=0
for arg in "$@"; do
	case "$arg" in
		--no-verify) DO_VERIFY=0 ;;
		--check)    DO_VERIFY=0; DRY_RUN=1 ;;
		-h|--help)  sed -n '2,30p' "$0"; exit 0 ;;
		*) echo "prepare.sh: unknown option '$arg' (try --help)" >&2; exit 2 ;;
	esac
done

# ── output helpers ───────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
	C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'
	C_BLU=$'\033[34m'; C_BLD=$'\033[1m'; C_OFF=$'\033[0m'
else
	C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''; C_OFF=''
fi

# Downloads $1 into a private temp dir and echoes its path. The temp dir is
# removed when the subshell exits.
fetch_to_temp() {
	local url="$1"
	local tmp
	tmp="$(mktemp -d)" || return 1
	trap 'rm -rf "$tmp"' EXIT
	local name="${url##*/}"
	info "downloading $url"
	if ! curl -fsSL "$url" -o "$tmp/$name"; then
		die "download failed: $url"
	fi
	printf '%s\n' "$tmp/$name"
}

step()  { printf '%s==>%s %s\n' "$C_BLU$C_BLD" "$C_OFF" "$*"; }
info()  { printf '    %s\n' "$*"; }
ok()    { printf '    %s✓%s %s\n' "$C_GRN" "$C_OFF" "$*"; }
warn()  { printf '    %s!%s %s\n' "$C_YEL" "$C_OFF" "$*" >&2; }
die()   { printf '%serror:%s %s\n' "$C_RED$C_BLD" "$C_OFF" "$*" >&2; exit 1; }

# ── privilege helper ─────────────────────────────────────────────────────────
# `sudo -n` first so a non-interactive run fails loudly instead of hanging on a
# password prompt; fall back to an interactive sudo when a terminal is present.

SUDO=""
if [[ "$(id -u)" -ne 0 ]]; then
	if command -v sudo >/dev/null 2>&1; then
		if sudo -n true 2>/dev/null; then
			SUDO="sudo -n"
		elif [[ -t 0 ]]; then
			SUDO="sudo"
		else
			warn "need root for system packages and passwordless sudo is unavailable"
			die "re-run as root, or install these yourself: build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev desktop-file-utils"
		fi
	fi
fi

as_root() { if [[ -n "$SUDO" ]]; then $SUDO "$@"; else "$@"; fi; }

# ── package manager ───────────────────────────────────────────────────────────
# Nobara/Fedora first (the project's primary target), then the rest.

PKG=""
PKG_INSTALL=""
PKG_QUIET=""
detect_pkg_manager() {
	if   command -v dnf5    >/dev/null 2>&1; then PKG="dnf5";    PKG_INSTALL=install
	elif command -v dnf     >/dev/null 2>&1; then PKG="dnf";     PKG_INSTALL=install
	elif command -v yum     >/dev/null 2>&1; then PKG="yum";     PKG_INSTALL=install
	elif command -v apt-get >/dev/null 2>&1; then PKG="apt-get"; PKG_INSTALL=install; PKG_QUIET="-y"
	elif command -v pacman  >/dev/null 2>&1; then PKG="pacman";  PKG_INSTALL=install
	elif command -v zypper  >/dev/null 2>&1; then PKG="zypper";  PKG_INSTALL=install
	else return 1
	fi
	return 0
}

# apt needs its lists refreshed once; other managers do not.
apt_lists_stale() {
	[[ "$PKG" != "apt-get" ]] && return 1
	local age
	age=$(stat -c %Y /var/lib/apt/lists 2>/dev/null || echo 0)
	[[ $(( $(date +%s) - age )) -gt 86400 ]]
}

pkg_install() {
	(($DRY_RUN)) && { info "[dry-run] would install: $*"; return 0; }
	if [[ "$PKG" == "apt-get" ]]; then
		apt_lists_stale && $SUDO apt-get update -qq || true
		as_root apt-get install -y --no-install-recommends "$@"
	elif [[ "$PKG" == "pacman" ]]; then
		as_root pacman -Sy --noconfirm --needed "$@"
	else
		as_root "$PKG" $PKG_INSTALL -y -q "$@"
	fi
}

# Translate a logical dependency name to the package name for this manager.
pkg_name() {
	local logical="$1"
	case "$PKG" in
		apt-get)
			case "$logical" in
				build-essential)       echo "build-essential" ;;
				pkg-config)            echo "pkgconf" ;;
				gtk3)                  echo "libgtk-3-dev" ;;
				webkit2gtk41)          echo "libwebkit2gtk-4.1-dev" ;;
				desktop-file-utils)    echo "desktop-file-utils" ;;
				hicolor-icon-theme)    echo "hicolor-icon-theme" ;;
				curl)                  echo "curl" ;;
				ca-certificates)       echo "ca-certificates" ;;
				*) echo "$logical" ;;
			esac ;;
		pacman)
			case "$logical" in
				build-essential)       echo "base-devel" ;;
				pkg-config)            echo "pkgconf" ;;
				gtk3)                  echo "gtk3" ;;
				webkit2gtk41)          echo "webkit2gtk-4.1" ;;
				desktop-file-utils)    echo "desktop-file-utils" ;;
				hicolor-icon-theme)    echo "hicolor-icon-theme" ;;
				*) echo "$logical" ;;
			esac ;;
		*)
			case "$logical" in
				build-essential)       echo "gcc" "make" ;;
				pkg-config)            echo "pkgconf-pkg-config" ;;
				gtk3)                  echo "gtk3-devel" ;;
				webkit2gtk41)          echo "webkit2gtk4.1-devel" ;;
				desktop-file-utils)    echo "desktop-file-utils" ;;
				hicolor-icon-theme)    echo "hicolor-icon-theme" ;;
				*) echo "$logical" ;;
			esac ;;
	esac
}

# ── 1. system libraries ──────────────────────────────────────────────────────

ensure_system_libs() {
	step "System build + runtime libraries"

	if ! detect_pkg_manager; then
		warn "no supported package manager found (dnf5/dnf/yum/apt-get/pacman/zypper)"
		warn "install manually: build-essential pkg-config libgtk-3-dev libwebkit2gtk-4.1-dev desktop-file-utils"
		return 0
	fi
	info "package manager: $PKG"

	# Wails needs a C toolchain and pkg-config to find GTK/WebKit.
	local need=()
	command -v gcc >/dev/null 2>&1 || need+=("$(pkg_name build-essential)")
	command -v pkg-config >/dev/null 2>&1 || need+=("$(pkg_name pkg-config)")
	command -v make >/dev/null 2>&1 || need+=("$(pkg_name build-essential)")

	# gtk+-3.0 is what the Wails Linux backend dlopens.
	pkg-config --exists gtk+-3.0 2>/dev/null || need+=("$(pkg_name gtk3)")
	# webkit2gtk-4.1 matches the webkit2_41 build tag used by this project.
	pkg-config --exists webkit2gtk-4.1 2>/dev/null || need+=("$(pkg_name webkit2gtk41)")

	command -v desktop-file-validate >/dev/null 2>&1 || need+=("$(pkg_name desktop-file-utils)")

	# Icon theme is required for the installed app to show an icon.
	if ! compgen -G "/usr/share/icons/hicolor" >/dev/null 2>&1; then
		need+=("$(pkg_name hicolor-icon-theme)")
	fi

	if ((${#need[@]} == 0)); then
		ok "all system libraries present"
		return 0
	fi

	info "missing: ${need[*]}"
	pkg_install "${need[@]}"
	ok "system libraries installed"
}

# ── 2. Go ────────────────────────────────────────────────────────────────────

# ── 2. Go ─────────────────────────────────────────────────────────────────────
#
# Selection logic lives in scripts/toolchain.sh because build.sh needs the exact
# same decision; the download/install is only here.

# handle_toolchain_switch — clear the Go build cache when the toolchain in use
# differs from the one that populated it.
#
# A cache shared between Go versions fails the next build with
#   compile: version "go1.27.1" does not match go tool version "go1.25.0"
# on every stdlib package, because those entries were compiled by the other
# toolchain. The cache is only an optimisation, so clearing it is always safe.
handle_toolchain_switch() {
	local version="$1"
	go_cache_matches "$version" && return 0
	local prev=""
	if [[ -f .prepare-stamp ]]; then
		prev="$(awk -F= '/^go=/ {print $2}' .prepare-stamp 2>/dev/null || true)"
	fi
	if [[ -n "$prev" && "$prev" != "$version" ]]; then
		warn "Go changed: ${prev} -> ${version}"
	fi
	go_cache_reset
	go_cache_mark "$version"
}

ensure_go() {
	step "Go toolchain (project pins $GO_VERSION)"

	# Why the exact version matters: the Wails CLI loads packages with
	# golang.org/x/tools, which reads the compiler's export data. A Go newer than
	# the one this Wails release was built against cannot be read, and the
	# failure names an unrelated package on each run:
	#
	#     internal error: package "fmt" without types was imported from <pkg>
	#
	# go.mod, CI and the Flatpak Dockerfile all pin $GO_VERSION, so matching it
	# is what makes a local build behave like CI's.
	if select_go "$GO_VERSION" >/dev/null; then
		if [[ "$GO_SELECTED_EXACT" == "yes" ]]; then
			ok "Go $GO_SELECTED_VERSION (matches go.mod)"
		else
			warn "Go $GO_SELECTED_VERSION found, but the project pins $GO_VERSION"
			if [[ "${PREFER_PINNED_GO:-1}" == "1" ]]; then
				info "installing the pinned $GO_VERSION instead (PREFER_PINNED_GO=0 to keep $GO_SELECTED_VERSION)"
				install_go
				return $?
			fi
			warn "keeping Go $GO_SELECTED_VERSION; 'wails build' may fail with"
			warn "  'internal error: package ... without types was imported'"
		fi
		handle_toolchain_switch "$GO_SELECTED_VERSION"
		return 0
	fi

	warn "no Go >= $GO_VERSION found; installing $GO_VERSION"
	install_go
}

install_go() {
	if [[ "${INSTALL_GO:-1}" == "0" ]]; then
		die "Go $GO_VERSION is required but INSTALL_GO=0"
	fi
	if ((DRY_RUN)); then
		info "[dry-run] would install Go $GO_VERSION into ${PREFIX}/go"
		return 0
	fi

	local os arch
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	arch="$(uname -m)"
	case "$arch" in
		x86_64|amd64) arch=amd64 ;;
		aarch64|arm64) arch=arm64 ;;
		*) die "unsupported architecture for the Go tarball: $arch" ;;
	esac

	local tarball="go${GO_VERSION}.${os}-${arch}.tar.gz"
	local url="https://go.dev/dl/${tarball}"
	# Download into a temp dir and clean it up from a subshell: an EXIT trap
	# there is scoped correctly, whereas a RETURN trap on this function fires
	# after the locals are already gone and `set -u` turns the cleanup itself
	# into a fatal "unbound variable".
	local tarball_path
	tarball_path="$(fetch_to_temp "https://go.dev/dl/${tarball}")" || return 1

	# A private prefix: never clobber a distro- or version-manager-managed Go.
	local godir="${PREFIX}/go"
	rm -rf "$godir"
	as_root tar -C "${PREFIX}" -xzf "$tarball_path"
	export PATH="$godir/bin:$PATH"
	ok "Go $GO_VERSION installed to $godir/bin/go"

	# Keep it reachable for interactive shells too, but do not touch a version
	# manager's shims: a stale shim would win over these links.
	mkdir -p "$HOME/.local/bin"
	for b in go gofmt; do
		[[ -e "$godir/bin/$b" ]] && ln -sf "$godir/bin/$b" "$HOME/.local/bin/$b"
	done
	info "linked go/gofmt into ~/.local/bin"

	# A freshly installed toolchain has certainly not populated the shared
	# cache, and the previous one may have.
	handle_toolchain_switch "$GO_VERSION"
}

# ── 3. Node ──────────────────────────────────────────────────────────────────

node_major() { node --version 2>/dev/null | sed 's/^v//' | cut -d. -f1; }

ensure_node() {
	step "Node.js (need >= ${NODE_MAJOR})"

	local have=""
	have="$(node_major || true)"

	if [[ -n "$have" ]] && [[ "$have" -ge "$NODE_MAJOR" ]]; then
		# Some installs ship `node` without `npm` on PATH (version managers,
		# split prefixes). npm normally lives next to the node binary.
		if ! command -v npm >/dev/null 2>&1; then
			local nodedir bindir
			nodedir="$(dirname "$(command -v node)")"
			if [[ -x "$nodedir/npm" ]]; then
				mkdir -p "$HOME/.local/bin"
				ln -sf "$nodedir/npm" "$HOME/.local/bin/npm"
				export PATH="$HOME/.local/bin:$PATH"
				ok "npm was missing; linked $nodedir/npm into ~/.local/bin"
			else
				warn "node is present but npm is not; install it or re-run with INSTALL_NODE=1"
			fi
		fi
		ok "Node v$(node --version | sed 's/^v//') ($(command -v node))"
		return 0
	fi

	if [[ -n "$have" ]]; then
		warn "found Node $(node --version), need >= ${NODE_MAJOR}"
	fi

	if [[ "${INSTALL_NODE:-0}" == "0" ]]; then
		if [[ -z "$have" ]]; then
			die "Node >= ${NODE_MAJOR} is required but INSTALL_NODE=0"
		fi
		warn "continuing with the installed Node $(node --version)"
		return 0
	fi
	if ((DRY_RUN)); then
		info "[dry-run] would install Node ${NODE_MAJOR}.x LTS"
		return 0
	fi

	local os arch
	os="$(uname -s | tr '[:upper:]' '[:lower:]')"
	arch="$(uname -m)"
	case "$arch" in
		x86_64|amd64) arch=x64 ;;
		aarch64|arm64) arch=arm64 ;;
	esac
	local plat="${os}-${arch}"

	local ver="" tarball=""
	# Ask the Node index for the newest LTS of the wanted major, so the version
	# is never hardcoded and never goes stale.
	ver="$(curl -fsSL https://nodejs.org/dist/index.json 2>/dev/null |
		python3 -c "
import json,sys
want=int(sys.argv[1])
for r in json.load(sys.stdin):
    v=r['version'].lstrip('v').split('.')
    if int(v[0])==want and r.get('lts'):
        print(r['version'].lstrip('v')); break
" "$NODE_MAJOR" 2>/dev/null || true)"

	if [[ -z "$ver" ]]; then
		warn "could not resolve a Node ${NODE_MAJOR} LTS release; skipping"
		return 0
	fi
	tarball="node-v${ver}-linux-${arch}.tar.xz"

	info "downloading node v$ver"
	local tarball_path
	tarball_path="$(fetch_to_temp "https://nodejs.org/dist/v${ver}/${tarball}")" || return 1

	local nodir="${PREFIX}/node-v${ver}"
	rm -rf "$nodir"
	mkdir -p "$nodir"
	tar -xJ -C "$nodir" --strip-components=1 "${tarball_path}"

	mkdir -p "$HOME/.local/bin"
	for b in node npm npx corepack; do
		[[ -e "$nodir/bin/$b" ]] || continue
		ln -sf "$nodir/bin/$b" "$HOME/.local/bin/$b"
	done
	export PATH="$HOME/.local/bin:$PATH"
	ok "Node v$ver installed to $nodir (linked into ~/.local/bin)"
}

# ── 4. Wails CLI ─────────────────────────────────────────────────────────────

ensure_wails() {
	step "Wails CLI ($WAILS_VERSION)"

	local have=""
	have="$(wails_version || true)"

	if [[ -n "$have" && "$have" == "$WAILS_VERSION" ]]; then
		ok "wails $have"
		return 0
	fi
	[[ -n "$have" ]] && info "found wails $have, want $WAILS_VERSION"

	if ((DRY_RUN)); then
		info "[dry-run] would install wails $WAILS_VERSION"
		return 0
	fi

	if ! command -v go >/dev/null 2>&1; then
		die "go is not on PATH; cannot install the wails CLI"
	fi

	# Pin the CLI to the module version. A mismatched CLI generates bindings
	# that differ from what the module expects, which shows up as confusing
	# compile errors in frontend/wailsjs.
	local gobin="${GOBIN:-$HOME/go/bin}"
	info "installing github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION"
	go install "github.com/wailsapp/wails/v2/cmd/wails@$WAILS_VERSION" ||
		die "go install failed for wails $WAILS_VERSION"

	mkdir -p "$HOME/.local/bin"
	ln -sf "$gobin/wails" "$HOME/.local/bin/wails"
	export PATH="$HOME/.local/bin:$PATH"
	ok "wails $WAILS_VERSION ($(command -v wails))"
}

# ── 5. frontend dependencies ─────────────────────────────────────────────────

ensure_frontend_deps() {
	step "Frontend dependencies"

	if ! command -v npm >/dev/null 2>&1; then
		warn "npm not found; skipping frontend install"
		return 0
	fi
	if [[ ! -f frontend/package.json ]]; then
		warn "frontend/package.json missing; skipping"
		return 0
	fi
	if ((DRY_RUN)); then
		# --check must not touch node_modules.
		if [[ -d frontend/node_modules ]]; then
			ok "frontend dependencies present"
		else
			info "[dry-run] would run npm ci"
		fi
		return 0
	fi

	# npm ci is reproducible but needs the lockfile; fall back to install.
	if [[ -f frontend/package-lock.json ]]; then
		if (cd frontend && npm ci --no-audit --no-fund); then
			ok "frontend dependencies installed (npm ci)"
		else
			(cd frontend && npm install --no-audit --no-fund) || die "npm ci failed"
			warn "npm ci failed; fell back to npm install"
		fi
	else
		(cd frontend && npm install --no-audit --no-fund) || die "npm install failed"
		warn "no package-lock.json; used npm install"
	fi
}

# ── 6. verification ──────────────────────────────────────────────────────────

verify() {
	step "Verifying the tree"
	local failed=0

	if go build -tags "$WAILS_BUILD_TAGS" ./... >/dev/null 2>&1; then
		ok "go build"
	else
		warn "go build failed (tags: $WAILS_BUILD_TAGS)"
		go build -tags "$WAILS_BUILD_TAGS" ./... 2>&1 | head -20 || true
		failed=1
	fi

	# Frontend checks need npm. Report it as a skipped check rather than letting
	# a bare "npm: command not found" escape from a command substitution.
	if ! command -v npm >/dev/null 2>&1; then
		warn "npm is not on PATH — skipping frontend checks"
		warn "install Node (>= ${NODE_MAJOR}) and re-run: ./prepare.sh"
		return 1
	fi

	(cd frontend && npx svelte-kit sync >/dev/null 2>&1) || true

	if (cd frontend && npm run check >/dev/null 2>&1); then
		ok "svelte-check"
	else
		warn "svelte-check reported problems"
		(cd frontend && npm run check 2>&1 | tail -20) || true
		failed=1
	fi

	# The offline-icon guard has no test framework behind it, so run it here.
	if [[ -f frontend/scripts/check-offline-icons.mjs ]]; then
		if (cd frontend && node scripts/check-offline-icons.mjs >/dev/null 2>&1); then
			ok "offline icon coverage"
		else
			warn "unresolvable offline icon names:"
			(cd frontend && node scripts/check-offline-icons.mjs 2>&1 | sed 's/^/      /') || true
			failed=1
		fi
	fi

	# The Wails build needs CGO plus GTK/WebKit at link time.
	if [[ "$PKG" != "" ]] && command -v pkg-config >/dev/null 2>&1; then
		if pkg-config --exists gtk+-3.0 && pkg-config --exists webkit2gtk-4.1; then
			ok "GTK3 + WebKit2GTK 4.1 discoverable by pkg-config"
		else
			warn "GTK3/WebKit2GTK 4.1 not found by pkg-config; the binary will not link"
			failed=1
		fi
	fi

	return $failed
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
	printf '%s%sEmail Hub — development environment%s\n' "$C_BLD" "$C_BLU" "$C_OFF"
	printf '%srepo: %s%s\n\n' "$C_BLD" "$REPO_ROOT" "$C_OFF"

	ensure_system_libs
	ensure_go
	ensure_node
	ensure_wails
	ensure_frontend_deps

	if ((DRY_RUN)); then
		step "Dry run — nothing was changed"
		return 0
	fi

	if ((DO_VERIFY)); then
		if ! verify; then
			printf '\n%s%sPrepare finished, but the tree is not build-ready.%s\n' \
				"$C_BLD" "$C_YEL" "$C_OFF" >&2
			printf 'The failing checks are listed above. Most common causes:\n' >&2
			printf '  - missing Node/npm   -> install Node >= %s, then: ./prepare.sh\n' "$NODE_MAJOR" >&2
			printf '  - GTK/WebKit missing -> see the pkg-config warning above\n' >&2
			printf '  - svelte-check errors-> run: cd frontend && npm run check\n' >&2
			exit 1
		fi
	fi

	# Record what we found so build.sh can report a stale environment instead
	# of silently rebuilding against the wrong toolchain.
	cat > .prepare-stamp <<EOF
prepared_at=$(date -Is)
go=$(go version 2>/dev/null | awk '{print $3}' || echo unknown)
node=$(node --version 2>/dev/null || echo unknown)
npm=$(npm --version 2>/dev/null || echo unknown)
wails=$(wails_version || echo unknown)
prefix=$PREFIX
EOF
	ok "stamp written to .prepare-stamp"

	printf '\n%s%sReady.%s Next: ./build.sh\n' "$C_BLD" "$C_GRN" "$C_OFF"
}

main "$@"
