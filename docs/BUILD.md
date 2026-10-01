# BUILD

**Status: short form.** The canonical build, test, release and troubleshooting reference
is [`OPERATIONS.md`](OPERATIONS.md). This file is the quick version; where the two
disagree, `OPERATIONS.md` wins.

## 0. Prerequisites

You need Go 1.25+, the Wails v2 CLI, Node.js + npm, and OAuth credentials.

```bash
cp .env.example .env          # fill in GOOGLE_CLIENT_ID/SECRET, MICROSOFT_CLIENT_ID
```

`make build` warns and continues if the OAuth credentials are empty; Gmail and Outlook
OAuth login then do not work, which is the intended path for password-auth local testing.

Linux also needs the WebKit 2.1 development headers — the `Makefile` sets
`BUILD_TAGS := webkit2_41` unconditionally, so the older 4.0 packages will not work:

```bash
sudo dnf install webkit2gtk4.1-devel gtk3-devel          # Fedora / Nobara
sudo apt install build-essential libgtk-3-dev libwebkit2gtk-4.1-dev   # Debian/Ubuntu
```

## 1. Build

```bash
make build       # -> build/bin/hsx2mail   (build/bin/Hsx2Mail.app on macOS)
make dev         # hot reload
make dev-race    # hot reload with the race detector
make generate    # regenerate frontend/wailsjs bindings
```

All targets and their outputs are listed in `OPERATIONS.md` §2.

## 1b. Application icon

All icons come from one canonical source, `brand/icon.svg`. Nothing is
hand-maintained: the artwork is rasterised at every size the app needs and
installed into the hicolor theme by `install.sh`.

```bash
# After editing brand/icon.svg, or after adding a new icon slot:
./build/icons/generate-icons.sh
```

| Target | Used by |
|---|---|
| `brand/icon.png` (256), `brand/icon-beautyline.png` (512) | brand assets |
| `build/appicon.png` | Wails bundler / window |
| `build/linux/hsx2mail.png` | Linux install source |
| `internal/tray/icon.png` (22) | system tray |
| `frontend/public/favicon.png`, `icon-256.png`, `icon.svg` | webview, taskbar, window chrome |
| `build/windows/icon.ico` (16…256) | Windows |

`install.sh` renders the full hicolor set (16/24/32/48/64/128/256/512) directly
from the SVG at install time and adds `hicolor/scalable/apps/<app-id>.svg`, so a
HiDPI panel gets a vector rather than an upscaled bitmap. If `rsvg-convert` is
missing it falls back to the single pre-rendered 256px PNG.

Requires `rsvg-convert` (`librsvg2-tools`); the `.ico` container additionally
needs Python with Pillow.

Note on contrast: the current artwork is a teal fill with a **black** outline. It
reads on light backgrounds and on dark ones via the fill, but the black outline —
and with it the envelope's fold line — disappears on a dark panel. Recolouring
the stroke (or shipping a symbolic variant for GNOME) is the fix if that matters.

## 2. Build and install the Flatpak (recommended on Linux)

```bash
sudo dnf install flatpak-builder        # Fedora
# sudo apt install flatpak-builder      # Ubuntu/Debian
# sudo pacman -S flatpak-builder        # Arch

flatpak remote-add --if-not-exists --user flathub https://flathub.org
flatpak install -y --user flathub org.gnome.Platform
flatpak install -y --user flathub org.freedesktop.Sdk.Extension.node24

make flatpak
ls build/bin/*.flatpak
```

**The bundle name is versioned.** `build/flatpak/build-local.sh` derives it from git:

```bash
VERSION=$(git describe --tags --exact-match 2>/dev/null || echo "dev")
BUNDLE_NAME="Hsx2Mail-${VERSION}.flatpak"
```

So on a tagged commit you get `build/bin/Hsx2Mail-v0.3.2.flatpak`; on an untagged tree
you get `build/bin/Hsx2Mail-dev.flatpak`. There is no unversioned
`build/bin/Hsx2Mail.flatpak` — installing that path fails.

```bash
flatpak install --user build/bin/Hsx2Mail-v0.3.2.flatpak
flatpak run io.github.beheoxinh.Hsx2Mail
```

The dev manifest (`build/flatpak/build-flatpak.sh`, via `make flatpak-dev`) uses the app
id `io.github.beheoxinh.Hsx2Mail-dev`.

Details, permissions and the Flathub submission guide:
[`build/flatpak/README.md`](../build/flatpak/README.md).

## 3. Tests and linters

```bash
make test        # go test ./...
make test-race   # go test -race -count=1 ./...
make vet         # go vet ./...
make lint        # golangci-lint + ESLint + svelte-check + offline icons + knip
make check       # build + vet + test + lint  ← the gate
```

`make check` is what CI-equivalent means here; run it before opening a PR. See
`OPERATIONS.md` §3.

## 4. Release

Version lives in five places and there is no `CHANGELOG.md`:

1. `app/state.go:30` — `const Version`
2. `wails.json:12` — `productVersion`
3. `frontend/package.json:4` — `version` (and `package-lock.json` via `npm install`)
4. `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml` — a new `<release>` entry
5. The git tag, which the Flatpak script reads

The full procedure, the release gate and rollback are in `OPERATIONS.md` §4 and
`RELEASE.md`.

## 5. When a build fails

| Symptom | Look at |
|---|---|
| `wails: command not found` | `go install github.com/wailsapp/wails/v2/cmd/wails@latest` |
| pkg-config cannot find `webkit2gtk-4.1` | You installed the 4.0 headers; see §0 |
| `flatpak-builder: command not found` | `sudo dnf install flatpak-builder` |
| OAuth login fails immediately | `.env` is empty or the client id is not a ldflags input — see §0 |
| Node SDK extension missing during a Flatpak build | `flatpak install --user flathub org.freedesktop.Sdk.Extension.node24` |
| Tests red after a schema change | `go run ./tools/db/schemadump > /tmp/schema_raw.txt && python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt`, then `docs/DATABASE.md` §10 |
