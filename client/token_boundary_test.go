package client

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const accessTokenField = "accessToken"

// Only these two take tokenMu, so the rule rejects every other reference rather
// than try to prove from the AST that a lock is held (issue #397).
var accessTokenAccessors = map[string]bool{"AccessToken": true, "SetAccessToken": true}

// Unexporting stops other packages; setHTTPHeader, the read that started #397,
// lives here. The AST match ignores comments and any receiver name.
func TestAccessTokenFieldStaysBehindTheAccessors(t *testing.T) {
	t.Parallel()

	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	declared := false
	var offenders []string
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}

		fset := token.NewFileSet()
		file, parseErr := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, parseErr)

		for _, field := range structFieldNames(file, "AlpaconClient") {
			assert.NotEqual(t, "AccessToken", field, "the access token field must not be exported")
			if field == accessTokenField {
				declared = true
			}
		}
		offenders = append(offenders, fieldRefsOutside(fset, file, accessTokenField, accessTokenAccessors)...)
	}

	// A renamed field would leave the walk nothing to find and pass vacuously.
	assert.True(t, declared, "AlpaconClient declares no unexported field named %q", accessTokenField)
	assert.Empty(t, offenders, "every access token reference must sit inside AccessToken() or SetAccessToken()")
}

func structFieldNames(file *ast.File, structName string) []string {
	var names []string
	ast.Inspect(file, func(n ast.Node) bool {
		spec, isType := n.(*ast.TypeSpec)
		if !isType || spec.Name.Name != structName {
			return true
		}
		structType, isStruct := spec.Type.(*ast.StructType)
		if !isStruct {
			return true
		}
		for _, declared := range structType.Fields.List {
			for _, ident := range declared.Names {
				names = append(names, ident.Name)
			}
		}
		return false
	})
	return names
}

// One top-level declaration at a time, so the enclosing function name is known
// without a second pass. A field declaration is an ast.Field, so types.go is clean.
func fieldRefsOutside(fset *token.FileSet, file *ast.File, field string, allowed map[string]bool) []string {
	var offenders []string
	for _, decl := range file.Decls {
		where := "a declaration outside any function"
		if fn, isFunc := decl.(*ast.FuncDecl); isFunc {
			if allowed[fn.Name.Name] {
				continue
			}
			where = fn.Name.Name
		}

		ast.Inspect(decl, func(n ast.Node) bool {
			var pos token.Pos
			switch node := n.(type) {
			case *ast.SelectorExpr:
				if node.Sel.Name == field {
					pos = node.Sel.Pos()
				}
			case *ast.KeyValueExpr:
				if key, isIdent := node.Key.(*ast.Ident); isIdent && key.Name == field {
					pos = key.Pos()
				}
			}
			if pos.IsValid() {
				offenders = append(offenders, fmt.Sprintf("%s in %s", fset.Position(pos), where))
			}
			return true
		})
	}
	return offenders
}
