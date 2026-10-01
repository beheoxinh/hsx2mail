# RELEASE — checklist

**Canonical procedure: [`OPERATIONS.md`](OPERATIONS.md) §4.** This file is the short
ordered checklist. Execute it top to bottom; the steps are in dependency order because
the git tag must exist before the Flatpak build reads it.

## 0. Preconditions

```bash
git status --porcelain          # must be empty
make check                      # build + vet + test + lint
```

## 1. Bump the version — five places, in this order

There is no single source of version truth, and **there is no `CHANGELOG.md`** in this
repository. Release notes live in the AppStream metainfo `<releases>` entries, which is
what Flathub renders.

| # | File | What to change |
|---|------|----------------|
| 1 | `app/state.go:30` | `const Version = "0.3.2"` → new version |
| 2 | `wails.json:12` | `"productVersion": "0.3.2"` → new version |
| 3 | `frontend/package.json:4` | `"version": "0.3.2"` → new version |
| 4 | `frontend/package-lock.json` | regenerate: `make frontend-deps`, then `git add` |
| 5 | `build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml` | add a new `<release version="…" date="YYYY-MM-DD">` as the **first** child of `<releases>` |

```bash
grep -rn '0\.3\.2' app/state.go wails.json frontend/package.json \
    build/flatpak/io.github.beheoxinh.Hsx2Mail.metainfo.xml
```

The Flatpak manifest has **no** version field: `build/flatpak/build-local.sh:78` reads
`git describe --tags --exact-match`, falling back to `dev`.

## 2. If migrations changed

```bash
go run ./tools/db/schemadump > /tmp/schema_raw.txt
python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt
git diff docs/DATABASE.md        # must be committed
```

Also update the hand-written sections the generator cannot infer: `DATABASE.md` §2
(PRAGMAs), §5 (foreign keys), §6 (transaction boundaries), §8 (runtime-added columns),
§12 (retention). Add rollback notes to `SQL_ROLLBACK.md` unless the migration is a bare
`DROP INDEX`. Detail in `DATABASE.md` §10.

## 3. Commit and tag

```bash
git commit -am "release: v0.3.3"
git tag -a v0.3.3 -m "v0.3.3"
git push origin main --follow-tags
```

Tag **before** building the Flatpak; otherwise the bundle is named `Hsx2Mail-dev.flatpak`.

## 4. Verify before you publish

```bash
./build/bin/hsx2mail --version        # must print the tag you just created
git diff --exit-code docs/DATABASE.md # no uncommitted schema drift
```

## 5. Build artefacts

```bash
make build                        # build/bin/hsx2mail  (or build/bin/Hsx2Mail.app)
make flatpak                      # build/bin/Hsx2Mail-v0.3.3.flatpak
make build-windows-installer      # build/bin/hsx2mail-amd64-installer.exe
```

Artefact names are listed in `OPERATIONS.md` §4.3. Note the **versioned** Flatpak
filename.

## 6. Publish

Flathub submission is a git push to the flathub repo, not a build step — see
[`build/flatpak/flathub/README.md`](../build/flatpak/flathub/README.md).

## 7. If you have to roll the release back

Code: re-tag the previous commit and rebuild.

Database: **migrations are forward-only** and the ledger cannot express a down path, so an
older binary will refuse to open a newer database with `ErrSchemaTooNew` — that refusal is
the intended behaviour, not a bug. The practical rollback for users is *reinstall the
previous version **and** restore their database backup*. A rollback script reconstructs a
schema and is lossy; a backup is lossless. Full explanation, plus the
`rollback-v39-to-v30.sql` procedure and the v40/v41/v42 notes, is in
[`SQL_ROLLBACK.md`](SQL_ROLLBACK.md).
