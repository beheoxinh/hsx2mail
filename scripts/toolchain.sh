#!/usr/bin/env bash
# scripts/toolchain.sh — shared toolchain resolution for prepare.sh / build.sh.
#
# Sourced, never executed.
#
# Why this exists: a developer machine usually carries several Go toolchains
# (distro package, version manager, /usr/local/go). Which one wins depends on
# PATH order, and the Wails CLI inherits PATH, so `wails build` compiles with
# whatever `go` happens to be first. When that Go is newer than the release the
# CLI was built against, the build dies with a message that looks unrelated to
# Go:
#
#     internal error: package "fmt" without types was imported from <some pkg>
#
# (a different package is named on each run, because it is a package-loading
# failure, not a compile error in any one package.)
#
# go.mod, CI and the Flatpak Dockerfile all pin the same Go version, so
# matching go.mod is what makes a local build behave like CI's.

# Version comparison for dotted numeric versions.
#   version_at_least 1.27.1 1.25.0  -> true
#   version_at_least 1.24.9 1.25.0  -> false
#
# `sort -V -C` is deliberately not used: it only reports whether the input is
# already ordered, so testing "have, want" answers "have <= want" and inverts
# every comparison.
version_at_least() {
	local have="${1%%[!0-9.]*}" want="${2%%[!0-9.]*}"
	[[ -z "$have" || -z "$want" ]] && return 1
	[[ "$(printf '%s\n%s\n' "$want" "$have" | sort -V | head -n1)" == "$want" ]]
}

# Print the Go version a `go` binary reports, without the "go" prefix.
go_version_of() {
	[[ -x "$1" ]] || return 1
	"$1" version 2>/dev/null | awk '{print $3}' | sed 's/^go//'
}

# Every `go` binary worth considering, most specific first.
go_candidates() {
	local c
	for c in "$(command -v go 2>/dev/null || true)" \
	         "${HOME}/.local/bin/go" \
	         "${PREFIX:-/usr/local}/go/bin/go" \
	         "/usr/local/go/bin/go" \
	         "/usr/lib/golang/bin/go" \
	         "/usr/lib/go/bin/go"; do
		[[ -n "$c" && -x "$c" ]] && printf '%s\n' "$c"
	done
	for c in "${HOME}"/.local/share/mise/installs/go/*/bin/go \
	         "${HOME}"/.asdf/installs/golang/*/go/bin/go; do
		[[ -x "$c" ]] && printf '%s\n' "$c"
	done
}

# go_version_required — the version go.mod declares.
go_version_required() {
	awk '/^go /{print $2; exit}' go.mod 2>/dev/null
}

# wails_version — first token of line 1, ANSI escapes stripped. `wails version`
# colours its output when attached to a TTY, and the version is a single token,
# so `awk '{print $2}'` yields nothing.
wails_version() {
	command -v wails >/dev/null 2>&1 || return 1
	wails version 2>/dev/null |
		head -1 |
		sed $'s/\033\[[0-9;]*m//g' |
		awk '{print $1}'
}

# select_go — put the Go that matches go.mod first on PATH, and echo its
# version. Preference order:
#   1. an exact go.mod match   (what CI uses)
#   2. the newest Go >= go.mod
# Nothing here downloads anything; that is prepare.sh's job.
select_go() {
	local want="${1:-$(go_version_required)}"
	[[ -z "$want" ]] && want=1.25.0

	local exact="" exactver="" newer="" newerver=""
	local c v
	while read -r c; do
		[[ -z "$c" ]] && continue
		v="$(go_version_of "$c" || true)"
		[[ -z "$v" ]] && continue
		if [[ "$v" == "$want" ]]; then
			exact="$c"; exactver="$v"
		elif version_at_least "$v" "$want"; then
			if [[ -z "$newerver" ]] || version_at_least "$v" "$newerver"; then
				newer="$c"; newerver="$v"
			fi
		fi
	done < <(go_candidates)

	local chosen="" chosenver=""
	if [[ -n "$exact" ]]; then
		chosen="$exact"; chosenver="$exactver"
	elif [[ -n "$newer" ]]; then
		chosen="$newer"; chosenver="$newerver"
	else
		return 1
	fi

	# A GOROOT exported by the developer's shell (mise, asdf, distro defaults)
	# would make the chosen `go` binary drive the *other* tree's compiler and
	# standard library. The symptom is confusing because the tool that reports
	# the mismatch is the one from GOROOT:
	#
	#     compile: version "go1.27.1" does not match go tool version "go1.25.0"
	#
	# so the `go` binary on PATH is right and the GOROOT is wrong. Clearing it
	# lets each `go` derive its own GOROOT from its own location. GOTOOLCHAIN is
	# cleared for the same reason: it can silently re-select a different `go`.
	unset GOROOT
	unset GOTOOLCHAIN

	export PATH="$(dirname "$chosen"):$PATH"
	GO_SELECTED_BIN="$chosen"
	GO_SELECTED_VERSION="$chosenver"
	GO_SELECTED_EXACT="no"
	[[ "$chosenver" == "$want" ]] && GO_SELECTED_EXACT="yes"
	printf '%s\n' "$chosenver"
}

# go_cache_matches — true when the Go build cache was last written by this same
# Go version. A cache shared between toolchains produces
#   compile: version "go1.27.1" does not match go tool version "go1.25.0"
# on the first build after a switch, because the cached stdlib entries were
# compiled by the other toolchain.
go_cache_matches() {
	local want="$1"
	local prev
	prev="$(go env GOCACHE 2>/dev/null || true)"
	[[ -z "$prev" ]] && return 0
	local marker="$prev/.hsx2mail-go-toolchain"
	[[ -f "$marker" ]] || return 1
	[[ "$(cat "$marker" 2>/dev/null)" == "$want" ]]
}

go_cache_mark() {
	local version="$1"
	local prev
	prev="$(go env GOCACHE 2>/dev/null || true)"
	[[ -z "$prev" ]] && return 0
	printf '%s\n' "$version" >"$prev/.hsx2mail-go-toolchain" 2>/dev/null || true
}

# go_cache_reset — clear the build cache after a toolchain switch. Safe: the
# cache is a pure optimisation, only its reuse across toolchains is unsafe.
go_cache_reset() {
	info "clearing the Go build cache (toolchain changed)"
	go clean -cache 2>/dev/null || true
}
