package cmd

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const modulePath = "github.com/alpacax/alpacon-cli"

// Referenced so a rename breaks the build instead of leaving the walk nothing to match.
var (
	_ = utils.ProcessEditedData
	_ = utils.ExitIfNoChanges
)

// An edit that changes nothing exits 0, but only when the command checks for ErrNoChanges
// before its own failure exit; without the check it exits 1 with "no changes made".
func TestEditorCommandsExitZeroWhenNothingChanged(t *testing.T) {
	t.Parallel()
	editors := editorFuncs(t)
	require.NotEmpty(t, editors, "no api function calls utils.ProcessEditedData")

	checked := 0
	for _, file := range parseGoFiles(t, ".") {
		imports := importNames(file.ast)
		eachFuncBody(file.ast, func(body *ast.BlockStmt) {
			var editorCall *ast.CallExpr
			handled := false
			directCalls(body, func(call *ast.CallExpr) {
				path, name, ok := calledFunc(call, imports)
				switch {
				case !ok:
				case editors[path+"."+name]:
					editorCall = call
				case path == modulePath+"/utils" && name == "ExitIfNoChanges":
					handled = true
				}
			})
			if editorCall == nil {
				return
			}
			checked++
			assert.True(t, handled, "%s calls an editor function without utils.ExitIfNoChanges",
				file.fset.Position(editorCall.Pos()))
		})
	}
	assert.Positive(t, checked, "no command calls an editor function")
}

type parsedFile struct {
	fset *token.FileSet
	ast  *ast.File
}

func parseGoFiles(t *testing.T, root string) []parsedFile {
	t.Helper()
	var files []parsedFile
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return err
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			return err
		}
		files = append(files, parsedFile{fset: fset, ast: f})
		return nil
	})
	require.NoError(t, err)
	return files
}

// editorFuncs returns "importpath.Func" for every api function that reaches ProcessEditedData,
// directly or through another function of its own package.
func editorFuncs(t *testing.T) map[string]bool {
	t.Helper()
	type decl struct {
		pkg     string
		body    *ast.BlockStmt
		imports map[string]string
	}
	decls := map[string]decl{}
	for _, file := range parseGoFiles(t, "../api") {
		dir := filepath.Dir(file.fset.Position(file.ast.Pos()).Filename)
		pkg := modulePath + "/api" + strings.TrimPrefix(filepath.ToSlash(dir), "../api")
		imports := importNames(file.ast)
		for _, d := range file.ast.Decls {
			if fn, ok := d.(*ast.FuncDecl); ok && fn.Recv == nil && fn.Body != nil {
				decls[pkg+"."+fn.Name.Name] = decl{pkg: pkg, body: fn.Body, imports: imports}
			}
		}
	}

	editors := map[string]bool{modulePath + "/utils.ProcessEditedData": true}
	for grew := true; grew; {
		grew = false
		for key, d := range decls {
			if editors[key] {
				continue
			}
			ast.Inspect(d.body, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok && reaches(call, d.pkg, d.imports, editors) {
					editors[key], grew = true, true
				}
				return !editors[key]
			})
		}
	}
	delete(editors, modulePath+"/utils.ProcessEditedData")
	return editors
}

// reaches reports whether call names a known editor function, qualified or in pkg itself.
func reaches(call *ast.CallExpr, pkg string, imports map[string]string, editors map[string]bool) bool {
	if path, name, ok := calledFunc(call, imports); ok {
		return editors[path+"."+name]
	}
	id, ok := call.Fun.(*ast.Ident)
	return ok && editors[pkg+"."+id.Name]
}

// importNames maps each import's name in the file to its path.
func importNames(f *ast.File) map[string]string {
	names := map[string]string{}
	for _, imp := range f.Imports {
		path, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path[strings.LastIndex(path, "/")+1:]
		if imp.Name != nil {
			name = imp.Name.Name
		}
		names[name] = path
	}
	return names
}

// calledFunc resolves a pkg.Func call to its import path and function name.
func calledFunc(call *ast.CallExpr, imports map[string]string) (string, string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", "", false
	}
	id, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", "", false
	}
	path, ok := imports[id.Name]
	return path, sel.Sel.Name, ok
}

// eachFuncBody visits the body of every function declaration and literal in f.
func eachFuncBody(f *ast.File, visit func(*ast.BlockStmt)) {
	ast.Inspect(f, func(n ast.Node) bool {
		switch fn := n.(type) {
		case *ast.FuncDecl:
			if fn.Body != nil {
				visit(fn.Body)
			}
		case *ast.FuncLit:
			visit(fn.Body)
		}
		return true
	})
}

// directCalls visits the calls in body that are not inside a nested function literal.
func directCalls(body *ast.BlockStmt, visit func(*ast.CallExpr)) {
	ast.Inspect(body, func(n ast.Node) bool {
		if _, ok := n.(*ast.FuncLit); ok {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			visit(call)
		}
		return true
	})
}
