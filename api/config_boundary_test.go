package api

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

const configPkgPath = "github.com/alpacax/alpacon-cli/config"

// configReaders answer a question out of the config file, LoadConfig by way of
// the other three calling it inside. Writes load it too but hand nothing back.
var configReaders = map[string]bool{
	"LoadConfig":           true,
	"IsSaaS":               true,
	"GetActiveWorkSession": true,
	"ResolveAuthMethod":    true,
}

// TestAPILayerNeverReadsConfig keeps the api layer off the config file: a read
// here can name a workspace the request is no longer going to (issue #401). It
// walks the AST, so a comment naming config.LoadConfig is not one and an alias is.
func TestAPILayerNeverReadsConfig(t *testing.T) {
	t.Parallel()
	var offenders []string

	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		local, imported := configImportName(file)
		if !imported {
			return nil
		}
		if local == "." {
			// A dot import leaves config.LoadConfig a bare identifier no selector
			// walk can attribute, so the import itself is the offense.
			offenders = append(offenders, "api/"+path+" dot-imports config")
			return nil
		}
		if name, found := readsConfig(file, local); found {
			// Repo-relative, since the walk starts in api/ and a CI log needs an openable path.
			offenders = append(offenders, "api/"+path+" calls config."+name)
		}
		return nil
	})

	require.NoError(t, err)
	assert.Empty(t, offenders, "take the workspace from the client instead of reading config here")
}

func configImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != configPkgPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return "config", true
	}
	return "", false
}

// readsConfig matches the selector, not the call, so handing a config reader off
// as a value counts too.
func readsConfig(file *ast.File, local string) (string, bool) {
	name := ""
	ast.Inspect(file, func(n ast.Node) bool {
		sel, ok := n.(*ast.SelectorExpr)
		if !ok || !configReaders[sel.Sel.Name] {
			return true
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == local {
			name = sel.Sel.Name
			return false
		}
		return true
	})
	return name, name != ""
}
