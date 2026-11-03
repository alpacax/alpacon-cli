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

// TestNoAssignmentToTimeLocal keeps time.Local fixed for the whole process: any goroutine that calls
// time.Now reads it, a handler a finished test left running included, so a test that pins a zone by
// assigning it races with that goroutine. Hand the formatter a *time.Location instead.
func TestNoAssignmentToTimeLocal(t *testing.T) {
	t.Parallel()
	const repoRoot = "../.."
	var offenders []string
	sawTime := false

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
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		timeName := ""
		for _, imp := range file.Imports {
			if p, _ := strconv.Unquote(imp.Path.Value); p == "time" {
				timeName = "time"
				if imp.Name != nil {
					timeName = imp.Name.Name
				}
			}
		}
		if timeName == "" {
			return nil
		}
		sawTime = true
		ast.Inspect(file, func(n ast.Node) bool {
			assign, ok := n.(*ast.AssignStmt)
			if !ok {
				return true
			}
			for _, lhs := range assign.Lhs {
				sel, ok := lhs.(*ast.SelectorExpr)
				if !ok || sel.Sel.Name != "Local" {
					continue
				}
				if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == timeName {
					offenders = append(offenders, fset.Position(assign.Pos()).String())
				}
			}
			return true
		})
		return nil
	})

	require.NoError(t, err)
	require.True(t, sawTime, "the walk reached no file that imports time; is repoRoot still the repository root?")
	assert.Empty(t, offenders)
}
