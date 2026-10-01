#!/usr/bin/env bash
# install.sh — build Email Hub and install (or overwrite) it on this Linux box.
#
# Safe to re-run: it replaces an existing installation in place and leaves user
# data (~/.local/share/hsx2mail, ~/.config/hsx2mail) untouched.
#
# What it installs
#   <prefix>/bin/hsx2mail              the app
#   <prefix>/bin/hsx2mail-creds        OAuth credential helper (read from the app)
#   <prefix>/share/icons/hicolor/<sz>/apps/io.github.beheoxinh.Hsx2Mail.png
#   <prefix>/share/applications/io.github.beheoxinh.Hsx2Mail.desktop
#   <autostart dir>/io.github.beheoxinh.Hsx2Mail.desktop   (only with --autostart)
#
# Usage:
#   ./install.sh                     build + install to /usr/local (system-wide)
#   ./install.sh --user              install under ~/.local (no sudo)
#   ./install.sh --prefix /opt/hsx2mail
#   ./install.sh --no-build          install an existing build/bin/hsx2mail
#   ./install.sh --autostart         also enable login autostart
#   ./install.sh --no-default-mail   do not claim x-scheme-handler/mailto
#   ./install.sh --uninstall         remove what this script installed
#
# Environment:
#   PREFIX   same as --prefix
#   FORCE=1  do not prompt for confirmation

set -euo pipefail

REPO_ROOT="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$REPO_ROOT"

# shellcheck source=scripts/toolchain.sh
. "$REPO_ROOT/scripts/toolchain.sh"

APP_ID="io.github.beheoxinh.Hsx2Mail"
DESKTOP_FILE="${APP_ID}.desktop"
ICON_FILE="${APP_ID}.png"

# ── output ───────────────────────────────────────────────────────────────────

if [[ -t 1 ]]; then
	C_RED=$'\033[31m'; C_GRN=$'\033[32m'; C_YEL=$'\033[33m'
	C_BLU=$'\033[34m'; C_BLD=$'\033[1m'; C_OFF=$'\033[0m'
else
	C_RED=''; C_GRN=''; C_YEL=''; C_BLU=''; C_BLD=''; C_OFF=''
fi

step() { printf '%s==>%s %s\n' "$C_BLU$C_BLD" "$C_OFF" "$*"; }
info() { printf '    %s\n' "$*"; }
ok()   { printf '    %s✓%s %s\n' "$C_GRN" "$C_OFF" "$*"; }
warn() { printf '    %s!%s %s\n' "$C_YEL" "$C_OFF" "$*" >&2; }
die()  { printf '%serror:%s %s\n' "$C_RED$C_BLD" "$C_OFF" "$*" >&2; exit 1; }

# ── options ──────────────────────────────────────────────────────────────────

SCOPE="system"          # system | user
DO_BUILD=1
DO_UNINSTALL=0
ENABLE_AUTOSTART=0
SET_DEFAULT_MAIL=1

while (($#)); do
	case "$1" in
		--user)            SCOPE="user" ;;
		--system)          SCOPE="system" ;;
		--prefix)          shift; PREFIX="${1:-}" ;;
		--prefix=*)        PREFIX="${1#--prefix=}" ;;
		--no-build)        DO_BUILD=0 ;;
		--autostart)       ENABLE_AUTOSTART=1 ;;
		--no-default-mail) SET_DEFAULT_MAIL=0 ;;
		--uninstall)       DO_UNINSTALL=1 ;;
		-h|--help)         sed -n '2,30p' "$0"; exit 0 ;;
		*) die "unknown option '$1' (try --help)" ;;
	esac
	shift || true
done

if [[ -n "${PREFIX:-}" ]]; then
	:
elif [[ "$SCOPE" == "user" ]]; then
	PREFIX="$HOME/.local"
else
	PREFIX="/usr/local"
fi

BIN_DIR="$PREFIX/bin"
APP_BIN="$BIN_DIR/hsx2mail"
CREDS_BIN="$BIN_DIR/hsx2mail-creds"
APP_DIR="$PREFIX/share/applications"
ICON_BASE="$PREFIX/share/icons/hicolor"

# ── privilege ────────────────────────────────────────────────────────────────

SUDO=""
need_root() { [[ "$SCOPE" == "system" && "$(id -u)" -ne 0 ]]; }

setup_sudo() {
	if ! need_root; then return 0; fi
	command -v sudo >/dev/null 2>&1 || die "installing to $PREFIX needs root and sudo is not installed"
	if sudo -n true 2>/dev/null; then
		SUDO="sudo -n"
	elif [[ -t 0 ]]; then
		SUDO="sudo"
	else
		die "needs root and passwordless sudo is unavailable; re-run as root, or use --user"
	fi
}

as_root() { if [[ -n "$SUDO" ]]; then $SUDO "$@"; else "$@"; fi; }

confirm() {
	[[ "${FORCE:-0}" == "1" ]] && return 0
	[[ -t 0 ]] || return 0   # non-interactive: proceed (the caller asked for it)
	local reply
	read -r -p "$1 [y/N] " reply
	[[ "$reply" =~ ^[Yy] ]]
}

# ── locate sources ───────────────────────────────────────────────────────────

# The desktop entry and icon are copied from the repo. The desktop file is
# generated, not checked in under its final name, so fall back to the repo copy
# and rewrite Exec/TryExec if needed.
find_desktop_source() {
	local candidates=(
		"build/linux/hsx2mail.desktop"
		"build/linux/${DESKTOP_FILE}"
	)
	local f
	for f in "${candidates[@]}"; do
		[[ -f "$f" ]] && { printf '%s\n' "$f"; return 0; }
	done
	return 1
}

find_icon_source() {
	local candidates=(
		"build/linux/${ICON_FILE}"
		"build/linux/hsx2mail.png"
		"brand/icon.png"
		"brand/icon-beautyline.png"
	)
	local f
	for f in "${candidates[@]}"; do
		[[ -f "$f" ]] && { printf '%s\n' "$f"; return 0; }
	done
	return 1
}

icon_size() {
	local f="$1"
	python3 - "$f" <<'PY' 2>/dev/null || printf '256\n'
import struct, sys
data = open(sys.argv[1], 'rb').read(33)
w, h = struct.unpack('>II', data[16:24])
print(w)
PY
}

# ── stop a running instance ──────────────────────────────────────────────────

# Overwriting a binary that is currently executing fails with ETXTBSY, and a
# running instance would keep serving the old build from memory. Both are
# avoidable by stopping it first.
stop_running() {
	local pids
	# `pgrep -x` matches the executable NAME only. Matching the full command
	# line with `pgrep -f '(^|/)hsx2mail( |$)'` looks equivalent but is not: any
	# unrelated process whose arguments merely contain the checkout path (a
	# language server, an MCP server run from this repo) matches, and the
	# SIGTERM below would kill it.
	pids="$(pgrep -x hsx2mail 2>/dev/null || true)"
	[[ -z "$pids" ]] && { info "no running instance"; return 0; }

	info "running instance(s): $(echo "$pids" | tr '\n' ' ')"
	if [[ "${FORCE:-0}" == "1" || -t 0 ]]; then
		if [[ "${FORCE:-0}" != "1" ]] && ! confirm "Stop the running app to replace it?"; then
			warn "leaving it running; the install will replace the files only"
			return 0
		fi
		# SIGTERM first so the app can close the DB cleanly and checkpoint WAL.
		kill -TERM $pids 2>/dev/null || true
		for _ in {1..30}; do
			sleep 0.2
			pgrep -x hsx2mail >/dev/null 2>&1 || { ok "stopped"; return 0; }
		done
		warn "still running after 6s; sending SIGKILL"
		kill -KILL $pids 2>/dev/null || true
		sleep 0.5
	fi
	ok "stopped"
}

# ── uninstall ────────────────────────────────────────────────────────────────

do_uninstall() {
	step "Uninstalling"
	setup_sudo

	local removed=0
	local f
	# The scalable entry is named after the app id; the rasterised ones are named
	# the same way in their size directories, so one glob plus the SVG covers
	# everything generate-icons.sh / do_install can create.
	for f in "$APP_BIN" "$CREDS_BIN" \
	         "$APP_DIR/$DESKTOP_FILE" \
	         "$ICON_BASE"/*/apps/"$ICON_FILE" \
	         "$ICON_BASE/scalable/apps/$APP_ID.svg"; do
		if [[ -e "$f" ]]; then
			as_root rm -f "$f"
			info "removed $f"
			removed=1
		fi
	done
	# Empty theme directories left behind are tidied up.
	rmdir "$ICON_BASE"/*/apps 2>/dev/null || true
	rmdir "$ICON_BASE"/* 2>/dev/null || true

	# Only remove the autostart entry if it belongs to THIS prefix. The XDG
	# autostart dir is per-user and shared by every install scope, so a
	# `--prefix /tmp/x --uninstall` must not delete the entry written by the
	# system-wide install.
	local autostart="${XDG_CONFIG_HOME:-$HOME/.config}/autostart/$DESKTOP_FILE"
	if [[ -f "$autostart" ]]; then
		if grep -qF "Exec=$APP_BIN" "$autostart" 2>/dev/null; then
			rm -f "$autostart"
			info "removed $autostart"
			removed=1
		else
			info "kept $autostart (it points at a different install)"
		fi
	fi

	command -v update-desktop-database >/dev/null 2>&1 &&
		as_root update-desktop-database "$APP_DIR" 2>/dev/null || true

	((removed)) || info "nothing was installed under $PREFIX"
	ok "uninstalled (user data in ~/.local/share/hsx2mail was left alone)"
}

# ── install ──────────────────────────────────────────────────────────────────

do_install() {
	step "Installing to $PREFIX"

	local app_src="build/bin/hsx2mail"
	local creds_src="build/bin/hsx2mail-creds"
	[[ -x "$app_src" ]]   || die "$app_src not found; run ./build.sh first"
	[[ -f "$creds_src" ]] || warn "$creds_src missing — OAuth sign-in will not work"

	local desktop_src icon_src
	desktop_src="$(find_desktop_source)" ||
		die "no desktop entry found under build/linux/"
	icon_src="$(find_icon_source)" ||
		die "no application icon found (build/linux/ or brand/)"

	setup_sudo
	stop_running

	# 1. binaries
	info "installing binaries to $BIN_DIR"
	as_root mkdir -p "$BIN_DIR"
	# `install` sets the mode atomically, which also sidesteps ETXTBSY on a
	# binary that is somehow still mapped.
	as_root install -m 0755 "$app_src" "$APP_BIN"
	[[ -f "$creds_src" ]] && as_root install -m 0755 "$creds_src" "$CREDS_BIN"
	ok "binaries installed"

	# 2. icons.
	#
	# A desktop environment picks the closest rendered size for every surface
	# (panel, launcher, file manager, notification), so shipping only one PNG
	# makes the launcher upscale or the panel downscale. Render the full hicolor
	# set from the canonical SVG, and install the SVG as `scalable` so a
	# HiDPI panel gets a crisp vector rather than an upscaled bitmap.
	local icon_src_svg="$REPO_ROOT/brand/icon.svg"
	local installed=0

	if command -v rsvg-convert >/dev/null 2>&1 && [[ -f "$icon_src_svg" ]]; then
		for sz in 16 24 32 48 64 128 256 512; do
			as_root mkdir -p "$ICON_BASE/${sz}x${sz}/apps"
			as_root rsvg-convert --width "$sz" --height "$sz" \
				--background-color=none --format=png \
				-o "$ICON_BASE/${sz}x${sz}/apps/$ICON_FILE" "$icon_src_svg"
			installed=$(( installed + 1 ))
		done
		info "rendered $installed icon sizes from brand/icon.svg"

		as_root mkdir -p "$ICON_BASE/scalable/apps"
		as_root install -m 0644 "$icon_src_svg" "$ICON_BASE/scalable/apps/$APP_ID.svg"
		ok "scalable SVG installed"
	else
		# No rsvg-convert (or no source): fall back to the pre-rendered PNG.
		warn "rsvg-convert unavailable; installing the single pre-rendered icon"
		local size icon_dst
		size="$(icon_size "$icon_src")"
		icon_dst="$ICON_BASE/${size}x${size}/apps/$ICON_FILE"
		as_root mkdir -p "$(dirname "$icon_dst")"
		as_root install -m 0644 "$icon_src" "$icon_dst"
		ok "icon installed (${size}x${size})"
	fi

	# 3. desktop entry
	info "installing desktop entry to $APP_DIR"
	as_root mkdir -p "$APP_DIR"
	# No RETURN trap: it fires after this function has returned and the local
	# is already gone, so with `set -u` the cleanup itself aborts the install.
	# Validate under the FINAL filename: desktop-file-validate rejects a file
	# whose name does not end in .desktop, so validating a bare mktemp path
	# always reports a spurious problem and hides the real ones.
	local tmpdir tmp_desktop
	tmpdir="$(mktemp -d)"
	tmp_desktop="$tmpdir/$DESKTOP_FILE"
	sed -e "s|^Exec=.*|Exec=$APP_BIN %U|" \
	    -e "s|^TryExec=.*|TryExec=$APP_BIN|" \
	    "$desktop_src" >"$tmp_desktop"

	if command -v desktop-file-validate >/dev/null 2>&1; then
		if desktop-file-validate "$tmp_desktop"; then
			ok "desktop entry validates"
		else
			warn "desktop-file-validate reported problems above; installing anyway"
		fi
	fi
	as_root install -m 0644 "$tmp_desktop" "$APP_DIR/$DESKTOP_FILE"
	ok "desktop entry installed"

	# 4. desktop + icon caches
	command -v update-desktop-database >/dev/null 2>&1 &&
		as_root update-desktop-database "$APP_DIR" 2>/dev/null || true
	command -v gtk-update-icon-cache >/dev/null 2>&1 &&
		as_root gtk-update-icon-cache -f -t "$ICON_BASE" 2>/dev/null || true
	ok "caches refreshed"

	# 5. autostart
	if ((ENABLE_AUTOSTART)); then
		local adir="${XDG_CONFIG_HOME:-$HOME/.config}/autostart"
		local autostart_file="$adir/$DESKTOP_FILE"
		info "enabling autostart in $adir"

		# The XDG autostart directory holds ONE entry per app id and is shared by
		# every install scope, so an entry written by a different prefix would be
		# silently replaced here. That is how "autostart stopped working" happens:
		# a test install to /tmp overwrote the entry for the real one.
		if [[ -f "$autostart_file" ]]; then
			local current
			current="$(grep -m1 '^Exec=' "$autostart_file" 2>/dev/null || true)"
			if [[ -n "$current" && "$current" != "Exec=$APP_BIN --start-hidden" ]]; then
				warn "an autostart entry already exists and points elsewhere:"
				warn "    $current"
				warn "it will be replaced by: Exec=$APP_BIN --start-hidden"
				if ! confirm "Replace the existing autostart entry?"; then
					warn "keeping the existing entry; autostart left unchanged"
					rm -rf "$tmpdir"
					return 0
				fi
			fi
		fi

		mkdir -p "$adir"
		local tmp_auto
		tmp_auto="$(mktemp)"
		# XDG autostart runs before the session is fully up; --start-hidden keeps
		# the window out of the way, and the app's own settings decide whether it
		# stays in the tray afterwards.
		sed -e "s|^Exec=.*|Exec=$APP_BIN --start-hidden|" \
		    -e "s|^TryExec=.*|TryExec=$APP_BIN|" \
		    "$tmp_desktop" >"$tmp_auto"
		install -m 0644 "$tmp_auto" "$autostart_file"
		rm -f "$tmp_auto"
		ok "autostart enabled -> $autostart_file"
	fi

	# 6. default mail handler
	if ((SET_DEFAULT_MAIL)) && command -v xdg-mime >/dev/null 2>&1; then
		if xdg-mime default "$DESKTOP_FILE" x-scheme-handler/mailto 2>/dev/null; then
			ok "mailto: handler set to $DESKTOP_FILE"
		else
			warn "could not set the mailto: handler"
		fi
	fi

	# Cleaned up here rather than right after the install step: the autostart
	# entry is derived from the same staged file.
	rm -rf "$tmpdir"
}

# ── verify ───────────────────────────────────────────────────────────────────

verify_install() {
	step "Verifying"

	local problems=0

	if [[ -x "$APP_BIN" ]]; then
		ok "binary present: $APP_BIN"
	else
		warn "binary missing: $APP_BIN"; problems=1
	fi

	if [[ -x "$CREDS_BIN" ]]; then
		ok "credential helper present: $CREDS_BIN"
	else
		warn "credential helper missing: $CREDS_BIN (OAuth sign-in will fail)"
		problems=1
	fi

	if [[ -f "$APP_DIR/$DESKTOP_FILE" ]]; then
		ok "desktop entry present"
		if command -v desktop-file-validate >/dev/null 2>&1; then
			desktop-file-validate "$APP_DIR/$DESKTOP_FILE" ||
				{ warn "installed desktop entry does not validate"; problems=1; }
		fi
	else
		warn "desktop entry missing: $APP_DIR/$DESKTOP_FILE"; problems=1
	fi

	# An icon that no theme can find shows as a blank entry in the launcher.
	if compgen -G "$ICON_BASE/*/apps/$ICON_FILE" >/dev/null 2>&1; then
		ok "icon installed"
	else
		warn "icon missing under $ICON_BASE"; problems=1
	fi

	# The app looks for the helper next to its own binary, so confirm the pair
	# is actually co-located rather than trusting that install ran.
	if [[ -x "$APP_BIN" && -x "$CREDS_BIN" ]]; then
		if [[ "$(dirname "$APP_BIN")" == "$(dirname "$CREDS_BIN")" ]]; then
			ok "app and credential helper are co-located (OAuth lookup will work)"
		else
			warn "app and credential helper are in different directories"
			problems=1
		fi
	fi

	# Smoke test the installed copy, not the build output.
	if command -v timeout >/dev/null 2>&1 && [[ -x "$APP_BIN" ]]; then
		if timeout 10 "$APP_BIN" --help >/dev/null 2>&1; then
			ok "installed binary runs"
		else
			warn "installed binary did not answer --help (a GUI app may need a display)"
		fi
	fi

	# User data must survive an overwrite; say so explicitly.
	local data="$HOME/.local/share/hsx2mail"
	[[ -d "$data" ]] && info "user data preserved: $data"

	return $problems
}

# ── main ─────────────────────────────────────────────────────────────────────

main() {
	printf '%s%sEmail Hub — install%s\n\n' "$C_BLD" "$C_BLU" "$C_OFF"

	if ((DO_UNINSTALL)); then
		do_uninstall
		return 0
	fi

	# Build unless told otherwise, or the artifact is missing.
	local need_build="$DO_BUILD"
	if ((DO_BUILD == 0)) && [[ ! -x build/bin/hsx2mail ]]; then
		warn "build/bin/hsx2mail is missing; building anyway"
		need_build=1
	fi
	if ((need_build)); then
		step "Building"
		./build.sh || die "build failed; nothing was installed"
	fi

	do_install

	if ! verify_install; then
		die "install completed with problems (see above)"
	fi

	printf '\n%s%sInstalled.%s\n' "$C_BLD" "$C_GRN" "$C_OFF"
	printf '   App:    %s\n' "$APP_BIN"
	printf '   Run:    hsx2mail\n'
	printf '   Remove: ./install.sh --uninstall\n\n'
}

main "$@"
