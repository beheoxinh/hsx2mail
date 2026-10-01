#!/usr/bin/env python3
"""Regenerate the generated sections of docs/DATABASE.md.

Usage, from the repository root:

    go run ./tools/db/schemadump > /tmp/schema_raw.txt
    python3 tools/db/gen-database-doc.py /tmp/schema_raw.txt

The schema dump is produced by actually running database.Migrate() against an empty
database, so the table and index inventories cannot drift from migrations.go. The
migration inventory itself is parsed straight out of migrations.go. Everything is
spliced between the <!--GEN:NAME--> / <!--/GEN:NAME--> markers in the doc, so running
this twice is a no-op.
"""
import re
import sys

REPO = __import__("os").path.dirname(__import__("os").path.dirname(
    __import__("os").path.dirname(__import__("os").path.abspath(__file__))))
DOC = REPO + "/docs/DATABASE.md"
MIGSRC = REPO + "/internal/database/migrations.go"
SCHEMA = sys.argv[1] if len(sys.argv) > 1 else "/tmp/schema_raw.txt"

# Hand-written replacements for migrations whose leading SQL comment is either empty
# or a bare "X table" header that says nothing on its own.
DESC_OVERRIDE = {
    1: "Baseline: accounts, identities, folders, messages, attachments, drafts, and their indexes",
    6: "Contact sources (CardDAV servers/accounts) + per-source address books",
    24: "PGP keypairs and per-sender key associations",
    25: "PGP keyserver (HKP) configuration",
    26: "accounts.sync_all_folders, folders.subscribed",
    27: "accounts.sync_folders_enabled",
    28: "accounts.shared_mailbox_parent_id",
}


def load_schema():
    """Return (ddl_by_object, columns, foreign_keys) from the schema dump.

    dump format written by the scratch tool:
        ### table <name> (tbl=<name>)
        <CREATE TABLE ...>
        ### index <name> (tbl=<owner>)
        <CREATE INDEX ...>
        ### COLS <name> | <col:type, ...>
        ### FK <child>.<col> -> <parent>(<pk>) ON DELETE <action>
    """
    ddl, cols, fks = {}, {}, []
    kind = name = None
    buf = []
    for line in open(SCHEMA).read().splitlines():
        if line.startswith("### "):
            if name:
                ddl[(kind, name)] = "\n".join(buf).strip()
            body = line[4:]
            kind = name = None
            buf = []
            if body.startswith("COLS "):
                nm, _, rest = body[5:].partition(" | ")
                cols[nm] = rest
            elif body.startswith("FK "):
                fks.append(body[3:])
            else:
                k, _, nm = body.partition(" ")
                kind, name = k, nm.split(" (")[0]
        else:
            buf.append(line)
    if name:
        ddl[(kind, name)] = "\n".join(buf).strip()
    return ddl, cols, fks


def migration_rows():
    src = open(MIGSRC).read()
    lines = src.splitlines()
    starts = [
        (i, int(re.match(r"\s*Version:\s*(\d+),\s*$", l).group(1)))
        for i, l in enumerate(lines)
        if re.match(r"\s*Version:\s*(\d+),\s*$", l)
    ]
    rows = []
    for k, (i, v) in enumerate(starts):
        end = starts[k + 1][0] if k + 1 < len(starts) else len(lines)
        body = "\n".join(lines[i:end])
        cm = re.findall(r"--\s*(.+)", body)
        desc = DESC_OVERRIDE.get(v) or re.sub(r"\s+", " ", cm[0].strip() if cm else "")
        objs = []
        for m in re.finditer(
            r"^\s*(CREATE TABLE(?:\s+IF\s+NOT\s+EXISTS)?|CREATE UNIQUE INDEX(?:\s+IF\s+NOT\s+EXISTS)?"
            r"|CREATE INDEX(?:\s+IF\s+NOT\s+EXISTS)?|DROP INDEX|DROP TABLE|ALTER TABLE|INSERT INTO|UPDATE)\s+([^\s(]+)",
            body, re.M | re.I,
        ):
            raw = re.sub(r"\s+", " ", m.group(1) + " " + m.group(2))
            kw, _, name = raw.partition(" ")
            objs.append((kw.upper(), name))
        seen, uniq = set(), []
        for o in objs:
            if o not in seen:
                seen.add(o)
                uniq.append(o)
        rows.append((v, i + 1, desc, uniq))
    return rows


def splice(doc, key, body):
    """Replace the block between <!--GEN:key--> and <!--/GEN:key--> (idempotent)."""
    begin, end = f"<!--GEN:{key}-->", f"<!--/GEN:{key}-->"
    pat = re.compile(re.escape(begin) + r".*?" + re.escape(end), re.S)
    if pat.search(doc):
        return pat.sub(begin + "\n" + body + "\n" + end, doc)
    plain = re.compile(re.escape(begin) + r"\n?")
    if plain.search(doc):
        return plain.sub(begin + "\n" + body + "\n" + doc[plain.search(doc).end():], doc)
    raise SystemExit(f"marker {begin} not found")


def _cap(names, limit=4):
    """Render a name list, collapsing past `limit` into a count."""
    names = [n for n in dict.fromkeys(names)]
    if len(names) <= limit:
        return ", ".join(f"`{n}`" for n in names)
    head = ", ".join(f"`{n}`" for n in names[:limit])
    return f"{head} (+{len(names) - limit} more)"


def main():
    ddl, cols, fks = load_schema()
    doc = open(DOC).read()

    # ---- migrations -----------------------------------------------------------
    out = []
    for v, line, desc, objs in migration_rows():
        tbls, idxs, other = [], [], []
        for kw, name in objs:
            if kw == "CREATE" and name.upper().startswith("TABLE "):
                tbls.append(name.split(None, 1)[1])
            elif kw == "CREATE" and "INDEX" in name.upper():
                idxs.append(name.split()[-1])
            else:
                other.append(f"{kw} {name}")
        cells = []
        if tbls:
            cells.append("new table " + _cap(tbls))
        if idxs:
            cells.append("index " + _cap(idxs))
        if other:
            cells.append(_cap(other))
        out.append(f"| {v} | {line} | {desc} | {'; '.join(cells) if cells else '—'} |")
    doc = splice(doc, "MIGRATIONS", "\n".join(out))

    # ---- columns --------------------------------------------------------------
    out = []
    for name in sorted(cols):
        out.append(f"- **`{name}`** — {cols[name]}")
    doc = splice(doc, "COLUMNS", "\n".join(out))

    # ---- indexes --------------------------------------------------------------
    manual = sorted(n for k, n in ddl if k == "index" and not n.startswith("sqlite_autoindex"))
    out = []
    for n in manual:
        sql = re.sub(r"\s+", " ", ddl[("index", n)]).strip()
        sql = re.sub(r"^CREATE (UNIQUE )?INDEX \S+\s+", "", sql)
        out.append(f"| `{n}` | `{sql}` |")
    doc = splice(doc, "INDEXES", "\n".join(out))

    open(DOC, "w").write(doc)
    print(f"migrations={len(migration_rows())} tables={len(cols)} indexes={len(manual)}")
    # A well-formed doc has one end marker for every begin marker. Prose mentions of
    # the marker syntax are excluded by only counting the exact begin/end forms.
    begins = doc.count("\n<!--GEN:")
    ends = doc.count("\n<!--/GEN:")
    print(f"markers: {begins} begin / {ends} end")
    if begins != ends:
        raise SystemExit("unbalanced generated-block markers")


if __name__ == "__main__":
    main()
