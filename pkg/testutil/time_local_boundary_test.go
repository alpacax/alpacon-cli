package testutil

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestNoAssignmentToTimeLocal fails on any write to time.Local: a goroutine a finished test left
// running still reads it through time.Now, so pinning a zone that way races. Pass a *time.Location.
func TestNoAssignmentToTimeLocal(t *testing.T) {
	t.Parallel()
	const repoRoot = "../.."
	var offenders []string
	sawWebsh := false

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != repoRoot && strings.HasPrefix(d.Name(), ".") {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}
		if filepath.ToSlash(path) == repoRoot+"/api/websh/websh.go" {
			sawWebsh = true
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		offenders = append(offenders, timeLocalWrites(fset, file)...)
		return nil
	})

	require.NoError(t, err)
	require.True(t, sawWebsh, "the walk never reached api/websh/websh.go; is repoRoot still the repository root?")
	assert.Empty(t, offenders)
}

func TestTimeLocalWrites_CatchesEveryWayToWriteIt(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		src    string
		caught bool
	}{
		{name: "assignment", src: `import "time"; func f() { time.Local = time.UTC }`, caught: true},
		{name: "parenthesized", src: `import "time"; func f() { (time.Local) = time.UTC }`, caught: true},
		{name: "through the pointer", src: `import "time"; func f() { *time.Local = *time.UTC }`, caught: true},
		{name: "address taken", src: `import "time"; func f() { p := &time.Local; *p = time.UTC }`, caught: true},
		{name: "aliased import", src: `import tm "time"; func f() { tm.Local = tm.UTC }`, caught: true},
		{name: "dot import", src: `import . "time"; func f() { _ = Now() }`, caught: true},
		{name: "read only", src: `import "time"; func f() { _ = time.Now().In(time.Local) }`, caught: false},
		{name: "another package's Local", src: `import "time"; type z struct{ Local *time.Location }; func f(v z) { v.Local = time.UTC }`, caught: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			fset := token.NewFileSet()
			file, err := parser.ParseFile(fset, "x.go", "package x; "+tt.src, 0)
			require.NoError(t, err)

			got := timeLocalWrites(fset, file)

			assert.Equal(t, tt.caught, len(got) > 0, "offenders: %v", got)
		})
	}
}

// timeLocalWrites lists where file writes time.Local, takes its address, or dot-imports time.
func timeLocalWrites(fset *token.FileSet, file *ast.File) []string {
	timeName := ""
	var found []string
	for _, imp := range file.Imports {
		if p, _ := strconv.Unquote(imp.Path.Value); p != "time" {
			continue
		}
		timeName = "time"
		if imp.Name != nil {
			timeName = imp.Name.Name
		}
		if timeName == "." {
			found = append(found, fset.Position(imp.Pos()).String())
		}
	}
	if timeName == "" || timeName == "." {
		return found
	}
	isTimeLocal := func(e ast.Expr) bool {
		for {
			switch v := e.(type) {
			case *ast.ParenExpr:
				e = v.X
			case *ast.StarExpr:
				e = v.X
			case *ast.SelectorExpr:
				pkg, ok := v.X.(*ast.Ident)
				return ok && pkg.Name == timeName && v.Sel.Name == "Local"
			default:
				return false
			}
		}
	}
	ast.Inspect(file, func(n ast.Node) bool {
		switch v := n.(type) {
		case *ast.AssignStmt:
			for _, lhs := range v.Lhs {
				if isTimeLocal(lhs) {
					found = append(found, fset.Position(v.Pos()).String())
				}
			}
		case *ast.UnaryExpr:
			if v.Op == token.AND && isTimeLocal(v.X) {
				found = append(found, fset.Position(v.Pos()).String())
			}
		}
		return true
	})
	return found
}
