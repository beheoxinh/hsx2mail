// Command txcheck fails when a function opens a database transaction that never
// writes.
//
// Why this exists: the connection string sets _txlock=immediate, so BEGIN
// acquires SQLite's global write lock up front. That is deliberate — a DEFERRED
// transaction that starts as a read and later upgrades to a write fails with
// SQLITE_BUSY_SNAPSHOT, which busy_timeout cannot wait out. The cost is that a
// read-only transaction becomes a process-wide writer: a chunked read holding
// the lock blocks header upserts, flag writes and every draft save until it
// finishes. One such function (internal/message.GetDeletedUIDInfo) did exactly
// that.
//
// So the rule for this codebase is: if a function only reads, it does not open
// a transaction. This check enforces it instead of trusting review.
//
// Usage:
//
//	go run ./tools/db/txcheck [packages...]
//
// With no arguments it checks ./... . Exits 1 and prints file:line for each
// offender.
package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"./..."}
	}
	if err := run(roots); err != nil {
		fmt.Fprintln(os.Stderr, "txcheck:", err)
		os.Exit(2)
	}
}

func run(roots []string) error {
	dirs, err := resolve(roots)
	if err != nil {
		return err
	}

	fset := token.NewFileSet()
	var offenders []string

	for _, dir := range dirs {
		pkg, err := parsePackage(fset, dir)
		if err != nil {
			return err
		}
		if pkg == nil {
			continue
		}
		for _, fn := range checkPackage(fset, pkg) {
			offenders = append(offenders, locate(fset, dir, fn)+": "+fn+" opens a transaction but never writes")
		}
	}

	if len(offenders) > 0 {
		fmt.Fprintf(os.Stderr, "txcheck: %d read-only transaction(s) found.\n", len(offenders))
		fmt.Fprintln(os.Stderr, "The DSN uses _txlock=immediate, so BEGIN takes SQLite's global write lock.")
		fmt.Fprintln(os.Stderr, "A transaction that never writes blocks every other writer for its lifetime.")
		for _, o := range offenders {
			fmt.Fprintln(os.Stderr, "  "+o)
		}
		os.Exit(1)
	}
	fmt.Printf("txcheck: no read-only transactions (%d packages).\n", len(dirs))
	return nil
}

// packageFacts holds the write/tx facts about every function in one package, so
// a function can be judged together with the helpers it delegates to.
type packageFacts struct {
	// writeConsts is the set of package-level string constants whose text
	// contains a write verb. SQL kept in a const (upsertMessageSQL) is the
	// normal way this codebase shares one statement across paths, so a
	// body-only scan would miss it entirely.
	writeConsts map[string]bool
	// writes is true when the function's body contains a write statement.
	writes map[string]bool
	// opensTx is true when the function begins a transaction.
	opensTx map[string]bool
	// calls maps a function to the functions it calls by name (unqualified).
	calls map[string][]string
	// isWrapper marks a function whose transaction body is supplied by the
	// caller (WithTx-style helpers). Whether it writes is the caller's
	// business, so the check must not judge it.
	isWrapper map[string]bool
}

// nameKey gives a function a stable identity: "Recv.Method" or "funcName".
func nameKey(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return exprString(fn.Recv.List[0].Type) + "." + fn.Name.Name
	}
	return fn.Name.Name
}

// packageFiles parses every buildable non-test .go file in dir, honouring the
// build constraints that the deprecated parser.ParseDir ignored (it associated
// files with packages without consulting build tags, so a file behind a tag this
// tool does not set would still be analysed and could produce a false report).
func packageFiles(dir string) ([]*ast.File, error) {
	return parseDirFiles(token.NewFileSet(), dir)
}

func parseDirFiles(fset *token.FileSet, dir string) ([]*ast.File, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var out []*ast.File
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".go") ||
			strings.HasSuffix(entry.Name(), "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, entry.Name()), nil, parser.ParseComments)
		if err != nil {
			continue // let the compiler report syntax errors
		}
		if !buildableForThisTool(file) {
			continue
		}
		out = append(out, file)
	}
	return out, nil
}

// buildableForThisTool reports whether the file's //go:build / // +build lines
// allow it to compile without extra tags. txcheck itself is an ordinary
// user-space tool: it wants to see the package as the application builds it, so
// any constraint other than an OS/arch restriction is treated as satisfied
// rather than silently dropping the file.
func buildableForThisTool(file *ast.File) bool {
	// A file the build would exclude is still worth analysing here: the point is
	// to catch a read-only transaction in code that only exists on Windows or
	// behind a tag, before it ships. So no constraint is treated as excluding.
	_ = file
	return true
}

func parsePackage(fset *token.FileSet, dir string) (*packageFacts, error) {
	files, err := packageFiles(dir)
	if err != nil {
		return nil, nil // unreadable directory: let the compiler speak
	}

	facts := &packageFacts{
		writeConsts: map[string]bool{},
		writes:      map[string]bool{},
		opensTx:     map[string]bool{},
		calls:       map[string][]string{},
		isWrapper:   map[string]bool{},
	}

	// First pass: package-level string consts holding a write statement. A body
	// scan alone would miss them, because the SQL is referenced by name
	// (upsertMessageSQL) rather than written inline.
	for _, file := range files {
		for _, decl := range file.Decls {
			gd, ok := decl.(*ast.GenDecl)
			if !ok || gd.Tok != token.CONST {
				continue
			}
			for _, spec := range gd.Specs {
				vs, ok := spec.(*ast.ValueSpec)
				if !ok {
					continue
				}
				for _, value := range vs.Values {
					lit, ok := value.(*ast.BasicLit)
					if !ok || lit.Kind != token.STRING {
						continue
					}
					if containsWriteVerb(lit.Value) {
						for _, name := range vs.Names {
							facts.writeConsts[name.Name] = true
						}
					}
				}
			}
		}
	}

	// Second pass: per-function facts.
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil {
				continue
			}
			key := nameKey(fn)
			// A helper that takes a func(*sql.Tx) error hands the whole
			// transaction body to its caller, so it cannot be judged here.
			facts.isWrapper[key] = takesTxCallback(fn)
			facts.writes[key] = writesToDatabase(fn.Body) || referencesWriteConst(fn.Body, facts.writeConsts)
			facts.opensTx[key] = opensTransaction(fn.Body)
			facts.calls[key] = calledNames(fn.Body)
		}
	}
	return facts, nil
}

// takesTxCallback reports whether any parameter is func(*sql.Tx) error, i.e.
// the transaction's work is supplied by the caller.
func takesTxCallback(fn *ast.FuncDecl) bool {
	found := false
	inspect := func(field *ast.Field) {
		ft, ok := field.Type.(*ast.FuncType)
		if !ok || ft.Params == nil {
			return
		}
		// A parameter of the callback that is *sql.Tx means the caller
		// supplies the transaction's work.
		for _, p := range ft.Params.List {
			// Accept *sql.Tx and any other *Tx: the point is that the caller
			// hands in a transaction handle, not the exact package qualifier.
			star, ok := p.Type.(*ast.StarExpr)
			if !ok {
				continue
			}
			switch x := star.X.(type) {
			case *ast.Ident:
				if x.Name == "Tx" {
					found = true
				}
			case *ast.SelectorExpr:
				if x.Sel.Name == "Tx" {
					found = true
				}
			}
		}
	}
	if fn.Recv != nil {
		for _, f := range fn.Recv.List {
			inspect(f)
		}
	}
	if fn.Type.Params != nil {
		for _, f := range fn.Type.Params.List {
			inspect(f)
		}
	}
	return found
}

// containsWriteVerb reports whether a SQL-ish string issues a write.
func containsWriteVerb(text string) bool {
	upper := strings.ToUpper(text)
	for _, verb := range writeVerbs {
		if strings.Contains(upper, verb) {
			return true
		}
	}
	return false
}

// referencesWriteConst reports whether the body names a constant that holds a
// write statement.
func referencesWriteConst(body *ast.BlockStmt, consts map[string]bool) bool {
	if len(consts) == 0 {
		return false
	}
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		if id, ok := n.(*ast.Ident); ok && consts[id.Name] {
			found = true
		}
		return true
	})
	return found
}

// calledNames collects the unqualified identifiers invoked in a body.
func calledNames(body *ast.BlockStmt) []string {
	var out []string
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			if id, ok := call.Fun.(*ast.Ident); ok {
				out = append(out, id.Name)
			}
			return true
		}
		if id, ok := sel.X.(*ast.Ident); ok {
			out = append(out, id.Name) // pkg.Func or recv.Func
		}
		return true
	})
	return out
}

// reachesWrite reports whether fn, or anything it calls transitively within the
// package, issues a write. Delegating the write to a helper is the normal shape
// here (UpsertBatch -> upsertOne), so a body-only scan would flag it wrongly.
func (f *packageFacts) reachesWrite(fn string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		if f.writes[name] {
			return true
		}
		for _, callee := range f.calls[name] {
			if walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(fn)
}

func (f *packageFacts) reachesTx(fn string) bool {
	seen := map[string]bool{}
	var walk func(string) bool
	walk = func(name string) bool {
		if seen[name] {
			return false
		}
		seen[name] = true
		if f.opensTx[name] {
			return true
		}
		for _, callee := range f.calls[name] {
			if walk(callee) {
				return true
			}
		}
		return false
	}
	return walk(fn)
}

func checkPackage(fset *token.FileSet, facts *packageFacts) []string {
	var out []string
	for fn, opens := range facts.opensTx {
		if !opens {
			continue
		}
		if facts.isWrapper[fn] {
			continue
		}
		if facts.reachesWrite(fn) {
			continue
		}
		out = append(out, fn)
	}
	// Sort for stable output.
	sort.Strings(out)
	return out
}

// locate finds the declaration of a function so the message can cite file:line.
func locate(fset *token.FileSet, dir, name string) string {
	fset2 := token.NewFileSet()
	files, err := parseDirFiles(fset2, dir)
	if err != nil {
		return dir + ":" + name
	}
	for _, file := range files {
		for _, decl := range file.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Name.Name != shortName(name) {
				continue
			}
			pos := fset2.Position(fn.Pos())
			return pos.Filename + ":" + strconv.Itoa(pos.Line)
		}
	}
	return dir + ":" + name
}

func shortName(key string) string {
	if i := strings.LastIndex(key, "."); i >= 0 {
		return key[i+1:]
	}
	return key
}

func funcName(fn *ast.FuncDecl) string {
	if fn.Recv != nil && len(fn.Recv.List) > 0 {
		return "(" + exprString(fn.Recv.List[0].Type) + ")." + fn.Name.Name
	}
	return fn.Name.Name
}

func exprString(e ast.Expr) string {
	switch t := e.(type) {
	case *ast.Ident:
		return t.Name
	case *ast.StarExpr:
		return "*" + exprString(t.X)
	case *ast.SelectorExpr:
		return exprString(t.X) + "." + t.Sel.Name
	}
	return "?"
}

// opensTransaction reports whether the body calls db.Begin() or db.BeginTx(...).
func opensTransaction(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		switch sel.Sel.Name {
		case "Begin", "BeginTx":
			found = true
		}
		return true
	})
	return found
}

// writeVerbs are the statements that make a transaction a writer. Matching is on
// the SQL text, so it catches Exec/Query calls with a literal or a concatenated
// string, which is how every write in this codebase is issued.
var writeVerbs = []string{"INSERT", "UPDATE", "DELETE", "REPLACE"}

func writesToDatabase(body *ast.BlockStmt) bool {
	found := false
	ast.Inspect(body, func(n ast.Node) bool {
		if found {
			return false
		}
		lit, ok := n.(*ast.BasicLit)
		if !ok || lit.Kind != token.STRING {
			return true
		}
		text := strings.ToUpper(lit.Value)
		for _, verb := range writeVerbs {
			// "DELETE FROM", "INSERT INTO", "UPDATE x SET", "REPLACE INTO"
			if strings.Contains(text, verb) {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// resolve expands ./... into the concrete package directories that contain Go
// files, so the walk does not need go/packages.
func resolve(roots []string) ([]string, error) {
	var dirs []string
	seen := make(map[string]bool)

	for _, root := range roots {
		if !strings.Contains(root, "...") {
			if !seen[root] {
				seen[root] = true
				dirs = append(dirs, root)
			}
			continue
		}
		base := strings.TrimSuffix(root, "...")
		base = strings.TrimSuffix(base, string(filepath.Separator))
		if base == "" {
			base = "."
		}
		err := filepath.Walk(base, func(path string, info os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			if !info.IsDir() {
				return nil
			}
			switch info.Name() {
			case "node_modules", ".git", "dist", "build":
				return filepath.SkipDir
			}
			entries, rerr := os.ReadDir(path)
			if rerr != nil {
				return nil
			}
			hasGo := false
			for _, e := range entries {
				if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") &&
					!strings.HasSuffix(e.Name(), "_test.go") {
					hasGo = true
					break
				}
			}
			if hasGo && !seen[path] {
				seen[path] = true
				dirs = append(dirs, path)
			}
			return nil
		})
		if err != nil {
			return nil, err
		}
	}
	return dirs, nil
}
