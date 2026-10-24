package config_test

// The tests that read a variable through the packages that import
// config, which a test inside config cannot do without an import cycle.

import (
	"bytes"
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"maps"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/mmedum/google-sheets-mcp/v4/internal/config"
	"github.com/mmedum/google-sheets-mcp/v4/internal/credentials"
	"github.com/mmedum/google-sheets-mcp/v4/internal/userconfig"
)

// A variable of spaces alone is unset, as an empty one is, and the spaces
// around a value are dropped, so a form field cleared to a space starts
// the server with the default. That holds for every variable the server
// reads: Define's through Build, and each one read outside Define through
// its reader. One read outside Define with no reader here fails, so a
// variable added there cannot miss the rule.
func TestASettingOfSpacesIsUnset(t *testing.T) {
	outside := map[string]func(t *testing.T, value string) string{
		config.EnvConfigDir: func(t *testing.T, value string) string {
			t.Setenv(config.EnvConfigDir, value)
			dir, err := userconfig.BaseDir()
			return fmt.Sprintf("%q %v", dir, err)
		},
		config.EnvRefreshToken: func(_ *testing.T, value string) string {
			s := credentials.Store{Profile: "p", Env: func(string) string { return value }}
			tok, src, err := s.Resolve()
			return fmt.Sprintf("%q %q %v", tok, src, err)
		},
	}
	// What Define reads, recorded from its lookups. Each variable is
	// Define's or has a reader above, so the floor on Vars holds this too.
	defined := map[string]bool{}
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(&bytes.Buffer{})
	config.Define(fs, func(name string) string { defined[name] = true; return "" })

	vars := config.Vars()
	if len(vars) < config.VarsFloor {
		t.Fatalf("Vars has %d variables, want at least %d", len(vars), config.VarsFloor)
	}
	for _, v := range vars {
		read := func(_ *testing.T, value string) string {
			c, err := config.Load(nil, func(name string) string {
				if name == v.Name {
					return value
				}
				return ""
			})
			return fmt.Sprintf("%+v %v", c, err)
		}
		if !defined[v.Name] {
			var ok bool
			if read, ok = outside[v.Name]; !ok {
				t.Errorf("%s is read outside Define, and this test has no reader for it", v.Name)
				continue
			}
			delete(outside, v.Name)
		}
		for _, pair := range [][2]string{{"  ", ""}, {" x ", "x"}} {
			if got, want := read(t, pair[0]), read(t, pair[1]); got != want {
				t.Errorf("%s=%q: %s\nwant %s, as %s=%q", v.Name, pair[0], got, want, v.Name, pair[1])
			}
		}
	}
	for name := range outside {
		t.Errorf("this test has a reader for %s, which Vars does not list as read outside Define", name)
	}
}

// Every GSHEETS_ variable the server's code spells is one EnvVars lists,
// so a variable read outside Define cannot escape the bundle and
// staleness gates. So is every one scripts/ spells: the live driver, the
// evals and the smoke test set them for the server, and a misspelling
// there tests a configuration nobody asked for. None is typed whole in a
// string literal: each is config's constant or EnvPrefix and its key, so
// the prefix is spelled once. And none is read with os.Getenv or
// os.LookupEnv rather than config.Lookup, which trims it as Build does.
// spelled says what a name is. The scan must read every root and find a
// name outside the config package, or it read too little.
func TestEveryVariableTheCodeNamesIsInEnvVars(t *testing.T) {
	known := map[string]bool{}
	for _, name := range config.EnvVars() {
		known[name] = true
	}
	found := map[string]string{}
	elsewhere := 0
	fset := token.NewFileSet()
	roots := []string{"../../cmd", "../../internal", "../../scripts"}
	files := map[string]int{}
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
				return err
			}
			f, err := parser.ParseFile(fset, path, nil, 0)
			if err != nil {
				return err
			}
			files[root]++
			outside := filepath.Dir(path) != filepath.Join("..", "..", "internal", "config")
			sp := spelled(f)
			for name, at := range sp.names {
				found[name] = fset.Position(at).String()
				if outside {
					elsewhere++
				}
			}
			for _, typed := range sp.typed {
				t.Errorf("%s types %s whole; use config's constant or config.EnvPrefix and its key",
					fset.Position(typed.at), typed.name)
			}
			for _, at := range sp.direct {
				t.Errorf("%s reads a server variable straight from the environment; read it through config.Lookup",
					fset.Position(at))
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, root := range roots {
		if files[root] == 0 {
			t.Errorf("the scan parsed no Go file under %s; it read too little", root)
		}
	}
	if elsewhere == 0 {
		t.Error("the scan found no GSHEETS_ name outside internal/config; it read too little")
	}
	for _, v := range slices.Sorted(maps.Keys(found)) {
		if !known[v] {
			t.Errorf("%s names %s, which EnvVars does not list", found[v], v)
		}
	}
}

// A name is a whole word of a string, or of a + chain of literals and
// EnvPrefix read in order. A run of them that another operand follows
// ends in a prefix the operand completes, not in a name, unless a
// character that ends a name comes between. A literal holding a whole
// name types it. os.Getenv or os.LookupEnv of a name, or of one of
// config's Env constants, reads it past config.Lookup.
func TestSpelledReadsNamesAndPrefixes(t *testing.T) {
	for _, tc := range []struct {
		expr         string
		names, typed []string
	}{
		{`EnvPrefix + "ENABLE_" + kind`, nil, nil},
		{`"set " + x + EnvPrefix + "NEW"`, []string{"GSHEETS_NEW"}, nil},
		{`config.EnvPrefix + "A" + "B"`, []string{"GSHEETS_AB"}, nil},
		{`EnvPrefix + "C=" + value`, []string{"GSHEETS_C"}, nil},
		{`"GSHEETS_D"`, []string{"GSHEETS_D"}, []string{"GSHEETS_D"}},
		{`"start with GSHEETS_E=true or " + x`, []string{"GSHEETS_E"}, []string{"GSHEETS_E"}},
		{`"GSHEETS_F" + x`, nil, []string{"GSHEETS_F"}},
		{`"GSHEETS_* variables"`, nil, nil},
		{`f(EnvPrefix + "G", "GSHEETS_" + key)`, []string{"GSHEETS_G"}, nil},
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x\n\nvar _ = "+tc.expr+"\n", 0)
		if err != nil {
			t.Fatalf("%s: %v", tc.expr, err)
		}
		sp := spelled(f)
		if len(sp.direct) > 0 {
			t.Errorf("%s: read straight from the environment", tc.expr)
		}
		if names := slices.Sorted(maps.Keys(sp.names)); !slices.Equal(names, tc.names) {
			t.Errorf("%s: names %q, want %q", tc.expr, names, tc.names)
		}
		var typed []string
		for _, n := range sp.typed {
			typed = append(typed, n.name)
		}
		if !slices.Equal(typed, tc.typed) {
			t.Errorf("%s: typed %q, want %q", tc.expr, typed, tc.typed)
		}
	}
	for expr, direct := range map[string]bool{
		`os.Getenv(config.EnvPrefix + "PROFILE")`:                true,
		`os.LookupEnv(config.EnvConfigDir)`:                      true,
		`os.Getenv(k)`:                                           false,
		`config.Lookup(os.Getenv, config.EnvPrefix + "PROFILE")`: false,
	} {
		f, err := parser.ParseFile(token.NewFileSet(), "x.go", "package x\n\nvar _ = "+expr+"\n", 0)
		if err != nil {
			t.Fatalf("%s: %v", expr, err)
		}
		if got := len(spelled(f).direct) > 0; got != direct {
			t.Errorf("%s: read straight from the environment %t, want %t", expr, got, direct)
		}
	}
}

// envName is a GSHEETS_ variable's name.
var envName = regexp.MustCompile(`^GSHEETS_[A-Z0-9][A-Z0-9_]*$`)

// nameRune is a character a variable's name may hold.
func nameRune(r rune) bool { return r == '_' || ('A' <= r && r <= 'Z') || ('0' <= r && r <= '9') }

// spelling is what one file spells: each GSHEETS_ name with where, each
// name a string literal types whole, in order, and each os.Getenv or
// os.LookupEnv of a server variable.
type spelling struct {
	names  map[string]token.Pos
	typed  []typedName
	direct []token.Pos
}

// typedName is a name a string literal types whole, and where.
type typedName struct {
	name string
	at   token.Pos
}

// spelled is what f spells. A name is a whole word of a string literal,
// or of a + chain of literals and EnvPrefix read in order: each run of
// them is one string, and any other operand ends the run and is read on
// its own. A run another operand follows ends in a prefix, as
// EnvPrefix + "ENABLE_" + kind does, so a word running to its end is not
// a name. One built from any other constant, or through a format verb,
// is not seen.
func spelled(f *ast.File) spelling {
	sp := spelling{names: map[string]token.Pos{}}
	words := func(s string) []string { return strings.FieldsFunc(s, func(r rune) bool { return !nameRune(r) }) }
	spell := func(s string, at token.Pos, completed bool) {
		w := words(s)
		if completed && len(w) > 0 && nameRune(rune(s[len(s)-1])) {
			w = w[:len(w)-1]
		}
		for _, word := range w {
			if envName.MatchString(word) {
				sp.names[word] = at
			}
		}
	}
	var visit func(n ast.Node) bool
	visit = func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.BinaryExpr:
			if n.Op != token.ADD {
				return true
			}
			run, at := "", n.Pos()
			for _, op := range operands(n) {
				if s, ok := joined(op); ok {
					if run == "" {
						at = op.Pos()
					}
					run += s
					continue
				}
				spell(run, at, true)
				run = ""
				ast.Inspect(op, visit)
			}
			spell(run, at, false)
			return false
		case *ast.BasicLit:
			if s, err := strconv.Unquote(n.Value); n.Kind == token.STRING && err == nil {
				spell(s, n.Pos(), false)
			}
		}
		return true
	}
	ast.Inspect(f, visit)
	ast.Inspect(f, func(n ast.Node) bool {
		if call, ok := n.(*ast.CallExpr); ok && readsEnv(call) && len(call.Args) == 1 && serverVariable(call.Args[0]) {
			sp.direct = append(sp.direct, call.Pos())
		}
		if lit, ok := n.(*ast.BasicLit); ok && lit.Kind == token.STRING {
			s, _ := strconv.Unquote(lit.Value)
			for _, word := range words(s) {
				if envName.MatchString(word) {
					sp.typed = append(sp.typed, typedName{word, lit.Pos()})
				}
			}
		}
		return true
	})
	return sp
}

// readsEnv reports whether call is os.Getenv or os.LookupEnv.
func readsEnv(call *ast.CallExpr) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	return ok && pkg.Name == "os" && (sel.Sel.Name == "Getenv" || sel.Sel.Name == "LookupEnv")
}

// serverVariable reports whether e names a server variable: a chain
// spelling one, or one of config's Env constants.
func serverVariable(e ast.Expr) bool {
	if s, ok := joined(e); ok {
		return slices.ContainsFunc(strings.FieldsFunc(s, func(r rune) bool { return !nameRune(r) }), envName.MatchString)
	}
	sel, ok := e.(*ast.SelectorExpr)
	return ok && strings.HasPrefix(sel.Sel.Name, "Env")
}

// operands is a + chain's operands in order, through parentheses.
func operands(e ast.Expr) []ast.Expr {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op == token.ADD {
			return append(operands(e.X), operands(e.Y)...)
		}
	case *ast.ParenExpr:
		if b, ok := e.X.(*ast.BinaryExpr); ok && b.Op == token.ADD {
			return operands(b)
		}
	}
	return []ast.Expr{e}
}

// joined is a + chain of string literals and EnvPrefix as one string.
func joined(e ast.Expr) (string, bool) {
	switch e := e.(type) {
	case *ast.BinaryExpr:
		if e.Op != token.ADD {
			return "", false
		}
		x, ok := joined(e.X)
		if !ok {
			return "", false
		}
		y, ok := joined(e.Y)
		return x + y, ok
	case *ast.ParenExpr:
		return joined(e.X)
	case *ast.BasicLit:
		s, err := strconv.Unquote(e.Value)
		return s, e.Kind == token.STRING && err == nil
	case *ast.Ident:
		return config.EnvPrefix, e.Name == "EnvPrefix"
	case *ast.SelectorExpr:
		return config.EnvPrefix, e.Sel.Name == "EnvPrefix"
	}
	return "", false
}
