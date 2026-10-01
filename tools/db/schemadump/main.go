// Command schemadump applies every pending migration to a throwaway database and
// prints the resulting schema as markdown-ready lines.
//
// It exists so docs/DATABASE.md can be regenerated from the real migration code
// rather than transcribed by hand, which is the drift the doc's "single source of
// truth" claim is supposed to prevent. See tools/db/gen-database-doc.py.
//
// Usage:
//
//	go run ./tools/db/schemadump > /tmp/schema_raw.txt
//	python3 tools/db/gen-database-doc.py
package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/beheoxinh/hsx2mail/internal/database"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "schemadump:", err)
		os.Exit(1)
	}
}

func run() error {
	dir, err := os.MkdirTemp("", "hsx2mail-schemadump")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	path := filepath.Join(dir, "schema.db")
	db, err := database.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	if err := db.Migrate(); err != nil {
		return err
	}

	if err := dumpObjects(db); err != nil {
		return err
	}
	if err := dumpVersion(db); err != nil {
		return err
	}
	return dumpColumnsAndForeignKeys(db)
}

// dumpObjects prints sqlite_master entries grouped by object kind. Tables and
// indexes are printed with their DDL so the doc generator can quote it verbatim.
func dumpObjects(db *database.DB) error {
	rows, err := db.Query(`SELECT type, name, COALESCE(sql, '')
		FROM sqlite_master
		WHERE name NOT LIKE 'sqlite_autoindex%'
		ORDER BY CASE type
			WHEN 'table' THEN 1 WHEN 'index' THEN 2
			WHEN 'trigger' THEN 3 WHEN 'view' THEN 4 ELSE 5 END, name`)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var kind, name, ddl string
		if err := rows.Scan(&kind, &name, &ddl); err != nil {
			return err
		}
		fmt.Printf("### %s %s\n%s\n", kind, name, strings.TrimRight(ddl, "\n"))
	}
	return rows.Err()
}

func dumpVersion(db *database.DB) error {
	var v int
	if err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM migrations").Scan(&v); err != nil {
		return err
	}
	fmt.Println("### SCHEMA_VERSION", v)
	return nil
}

// dumpColumnsAndForeignKeys prints PRAGMA table_info and PRAGMA foreign_key_list for
// every application table. FTS5 shadow tables and sqlite_sequence are excluded: they
// are not application-owned.
func dumpColumnsAndForeignKeys(db *database.DB) error {
	tables, err := appTables(db)
	if err != nil {
		return err
	}
	for _, table := range tables {
		if err := dumpColumns(db, table); err != nil {
			return err
		}
		if err := dumpForeignKeys(db, table); err != nil {
			return err
		}
	}
	return nil
}

func appTables(db *database.DB) ([]string, error) {
	rows, err := db.Query(`SELECT name FROM sqlite_master
		WHERE type = 'table'
		  AND name NOT LIKE 'sqlite_%'
		  AND name NOT LIKE 'messages_fts%'
		ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}

func dumpColumns(db *database.DB, table string) error {
	rows, err := db.Query("PRAGMA table_info(" + quote(table) + ")")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	var cols []string
	for rows.Next() {
		var cid, notNull, pk int
		var name, colType string
		var defaultValue any
		if err := rows.Scan(&cid, &name, &colType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		cols = append(cols, name+":"+colType)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	fmt.Printf("### COLS %s | %s\n", table, strings.Join(cols, ", "))
	return nil
}

func dumpForeignKeys(db *database.DB, table string) error {
	rows, err := db.Query("PRAGMA foreign_key_list(" + quote(table) + ")")
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()

	for rows.Next() {
		var id, seq int
		var parent, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &parent, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return err
		}
		fmt.Printf("### FK %s.%s -> %s(%s) ON DELETE %s\n", table, from, parent, to, onDelete)
	}
	return rows.Err()
}

// quote makes a table name safe to interpolate into a PRAGMA argument. The names come
// from sqlite_master rather than user input, but SQLite has no bound-parameter form
// for PRAGMA arguments and a stray quote would silently produce a wrong answer.
func quote(name string) string {
	return "'" + strings.ReplaceAll(name, "'", "''") + "'"
}
