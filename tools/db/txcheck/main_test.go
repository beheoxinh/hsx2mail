package main

import (
	"go/ast"
	"go/parser"
	"go/token"
	"strings"
	"testing"
)

func parseSrc(t *testing.T, src string) (*token.FileSet, *ast.File) {
	t.Helper()
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "probe.go", src, 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return fset, f
}

func mustParse(t *testing.T, src string) *ast.File {
	t.Helper()
	_, f := parseSrc(t, src)
	return f
}

func declNames(t *testing.T, f *ast.File) map[string]*ast.FuncDecl {
	t.Helper()
	out := map[string]*ast.FuncDecl{}
	for _, d := range f.Decls {
		if fn, ok := d.(*ast.FuncDecl); ok {
			out[fn.Name.Name] = fn
		}
	}
	return out
}

func TestOpensTransaction_DetectsBeginAndBeginTx(t *testing.T) {
	cases := map[string]struct {
		src  string
		want bool
	}{
		"Begin": {`package p
func f(db *DB) error { _, err := db.Begin(); return err }`, true},
		"BeginTx": {`package p
func f(db *DB) error { _, err := db.BeginTx(ctx, nil); return err }`, true},
		"no tx": {`package p
func f(db *DB) error { _, err := db.Exec(q); return err }`, false},
	}
	for name, tc := range cases {
		_, f := parseSrc(t, tc.src)
		fn := declNames(t, f)["f"]
		if fn == nil {
			t.Fatalf("%s: could not find func f", name)
		}
		if got := opensTransaction(fn.Body); got != tc.want {
			t.Errorf("%s: opensTransaction = %v, want %v", name, got, tc.want)
		}
	}
}

func TestWritesToDatabase_SeeksVerbInSQLText(t *testing.T) {
	cases := map[string]bool{
		"INSERT INTO messages (id) VALUES (?)":                      true,
		"UPDATE messages SET body_fetched = 1 WHERE id = ?":         true,
		"DELETE FROM messages WHERE folder_id = ?":                  true,
		"REPLACE INTO settings (k, v) VALUES (?, ?)":                true,
		"SELECT id FROM messages WHERE folder_id = ?":               false,
		"SELECT COUNT(*) FROM drafts WHERE attachments_data LIKE ?": false,
	}
	for sql, want := range cases {
		src := "package p\nfunc f(tx *Tx) error { _, err := tx.Exec(`" + sql + "`); return err }"
		_, f := parseSrc(t, src)
		fns := declNames(t, f)
		if got := writesToDatabase(fns["f"].Body); got != want {
			t.Errorf("%q: writesToDatabase = %v, want %v", sql, got, want)
		}
	}
}

func TestTakesTxCallback(t *testing.T) {
	cases := map[string]struct {
		src  string
		want bool
	}{
		"WithTx style": {
			src: `package p
func (s *Store) WithTx(fn func(tx *sql.Tx) error) error { return nil }`,
			want: true,
		},
		"plain function": {
			src: `package p
func (s *Store) Save(id string) error { return nil }`,
			want: false,
		},
		"callback without tx param": {
			src: `package p
func (s *Store) Each(fn func(int) error) error { return nil }`,
			want: false,
		},
	}
	for name, tc := range cases {
		_, f := parseSrc(t, tc.src)
		fns := declNames(t, f)
		for _, fn := range fns {
			if got := takesTxCallback(fn); got != tc.want {
				t.Errorf("%s: takesTxCallback = %v, want %v", name, got, tc.want)
			}
			break
		}
	}
}

func TestContainsWriteVerb(t *testing.T) {
	if !containsWriteVerb("INSERT INTO x (a) VALUES (?)") {
		t.Error("INSERT not detected")
	}
	if containsWriteVerb("SELECT * FROM x WHERE a = ?") {
		t.Error("SELECT wrongly detected as a write")
	}
	// Case-insensitive: SQL can be written either way.
	if !containsWriteVerb("delete from messages where id = ?") {
		t.Error("lowercase delete not detected")
	}
}

func TestReferencesWriteConst(t *testing.T) {
	consts := map[string]bool{"upsertMessageSQL": true}
	cases := map[string]struct {
		body string
		want bool
	}{
		"uses const": {
			body: `_, err := q.Exec(upsertMessageSQL, args)`,
			want: true,
		},
		"unrelated const": {
			body: `_, err := q.Exec(listSQL, args)`,
			want: false,
		},
	}
	for name, tc := range cases {
		_, f := parseSrc(t, "package p\nfunc f(q rowQueryer) error {"+tc.body+"; return err }")
		fns := declNames(t, f)
		if got := referencesWriteConst(fns["f"].Body, consts); got != tc.want {
			t.Errorf("%s: referencesWriteConst = %v, want %v", name, got, tc.want)
		}
	}
}

// The regression this whole tool exists for: a read-only lookup wrapped in a
// transaction, which under _txlock=immediate blocks every other writer.
func TestCheckPackage_FlagsReadOnlyTransaction(t *testing.T) {
	src := `package p

type Tx struct{}
type DB struct{}

func (db *DB) Begin() (*Tx, error) { return nil, nil }

// readOnly opens a transaction and only ever selects.
func (db *DB) readOnly(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	rows, err := tx.Query(` + "`SELECT id FROM t WHERE x = ?`" + `, id)
	if err != nil {
		return err
	}
	return rows.Close()
}

// writer opens a transaction and deletes.
func (db *DB) writer(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec(` + "`DELETE FROM t WHERE x = ?`" + `, id); err != nil {
		return err
	}
	return tx.Commit()
}

// delegator opens a transaction but the write lives in a helper.
func (db *DB) delegator(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return writeIt(tx, id)
}

func writeIt(tx *Tx, id string) error {
	_, err := tx.Exec(` + "`DELETE FROM t WHERE x = ?`" + `, id)
	return err
}

// constUser opens a transaction and writes via a shared SQL constant.
func (db *DB) constUser(id string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(deleteSQL, id)
	return err
}

const deleteSQL = ` + "`DELETE FROM t WHERE x = ?`" + `

// wrapper delegates the whole transaction body to its caller.
func (db *DB) wrapper(fn func(*Tx) error) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	return fn(tx)
}
`
	_, f := parseSrc(t, src)

	// Build the same facts parsePackage would build.
	facts := &packageFacts{
		writeConsts: map[string]bool{"deleteSQL": true},
		writes:      map[string]bool{},
		opensTx:     map[string]bool{},
		calls:       map[string][]string{},
		isWrapper:   map[string]bool{},
	}
	for _, decl := range f.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if ok && gen.Tok == token.CONST {
			for _, spec := range gen.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, v := range vs.Values {
					if lit, ok := v.(*ast.BasicLit); ok && containsWriteVerb(lit.Value) {
						for _, n := range vs.Names {
							facts.writeConsts[n.Name] = true
						}
					}
				}
			}
		}
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok || fn.Body == nil {
			continue
		}
		key := nameKey(fn)
		facts.isWrapper[key] = takesTxCallback(fn)
		facts.writes[key] = writesToDatabase(fn.Body) || referencesWriteConst(fn.Body, facts.writeConsts)
		facts.opensTx[key] = opensTransaction(fn.Body)
		facts.calls[key] = calledNames(fn.Body)
	}

	flagged := map[string]bool{}
	for _, fn := range checkPackage(token.NewFileSet(), facts) {
		flagged[fn] = true
	}

	if !flagged["*DB.readOnly"] {
		t.Error("read-only transaction was not flagged")
	}
	for _, shouldPass := range []string{"*DB.writer", "*DB.delegator", "*DB.constUser", "*DB.wrapper"} {
		if flagged[shouldPass] {
			t.Errorf("%s was wrongly flagged", shouldPass)
		}
	}
}

func TestShortName(t *testing.T) {
	if got := shortName("*Store.UpsertBatch"); got != "UpsertBatch" {
		t.Errorf("shortName = %q, want UpsertBatch", got)
	}
	if got := shortName("plainFunc"); got != "plainFunc" {
		t.Errorf("shortName = %q, want plainFunc", got)
	}
}

func TestReachesWriteFollowsTransitiveCalls(t *testing.T) {
	src := `package p
func a() error { return b() }
func b() error { return c() }
func c() error { return nil }
func d() error { return e() }
func e() error { _, err := q.Exec(` + "`DELETE FROM t`" + `); return err }`
	_, f := parseSrc(t, src)
	facts := &packageFacts{
		writeConsts: map[string]bool{},
		writes:      map[string]bool{},
		opensTx:     map[string]bool{},
		calls:       map[string][]string{},
		isWrapper:   map[string]bool{},
	}
	for _, decl := range f.Decls {
		fn, ok := decl.(*ast.FuncDecl)
		if !ok {
			continue
		}
		facts.writes[fn.Name.Name] = writesToDatabase(fn.Body)
		facts.calls[fn.Name.Name] = calledNames(fn.Body)
	}
	if !facts.reachesWrite("d") {
		t.Error("reachesWrite(d) = false, want true (d -> e writes)")
	}
	if facts.reachesWrite("a") {
		t.Error("reachesWrite(a) = true, want false (a -> b -> c do not write)")
	}
}

func TestCalledNames(t *testing.T) {
	fns := declNames(t, mustParse(t, `package p
func f() error {
	_ = helper()
	_ = pkg.Other()
	var x *T
	_ = x.Method()
	return nil
}`))
	got := strings.Join(calledNames(fns["f"].Body), ",")
	for _, want := range []string{"helper", "pkg", "x"} {
		if !strings.Contains(got, want) {
			t.Errorf("calledNames = %q, missing %q", got, want)
		}
	}
}
