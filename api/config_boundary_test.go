package api

import (
	"go/ast"
	"go/parser"
	"go/token"
	"sort"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alpacax/alpacon-cli/config"
)

const configPkgPath = "github.com/alpacax/alpacon-cli/config"

// configReaders answer a question out of the config file, LoadConfig by way of
// the others calling it inside. Writes load it too but hand nothing back.
var configReaders = map[string]bool{
	"LoadConfig":              true,
	"IsSaaS":                  true,
	"GetActiveWorkSession":    true,
	"GetActiveWorkSessionFor": true,
	"ResolveAuthMethod":       true,
}

// The walk matches on name, so renaming a reader would leave it nothing to find
// and the guard would pass with nothing checked. These break the build instead.
var (
	_ = config.LoadConfig
	_ = config.IsSaaS
	_ = config.GetActiveWorkSession
	_ = config.GetActiveWorkSessionFor
	_ = config.ResolveAuthMethod
)

// TestAPILayerNeverReadsConfig keeps the api layer off the config file: a read here can name a
// workspace the request is no longer going to. It walks the AST, so a comment naming
// config.LoadConfig is not one and an alias is.
func TestAPILayerNeverReadsConfig(t *testing.T) {
	t.Parallel()
	pkgs, err := nonTestGoFiles("./...")
	require.NoError(t, err)

	var offenders []string
	for _, paths := range pkgs {
		for _, path := range paths {
			file, err := parser.ParseFile(token.NewFileSet(), path, nil, 0)
			require.NoError(t, err)
			local := importName(file, configPkgPath)
			if local == "" {
				continue
			}
			if local == "." {
				// A dot import leaves config.LoadConfig a bare identifier no selector
				// walk can attribute, so the import itself is the offense.
				offenders = append(offenders, "api/"+path+" dot-imports config")
				continue
			}
			if name, found := readsConfig(file, local); found {
				// Repo-relative, since the walk starts in api/ and a CI log needs an openable path.
				offenders = append(offenders, "api/"+path+" calls config."+name)
			}
		}
	}
	sort.Strings(offenders)

	assert.Empty(t, offenders, "take the workspace from the client instead of reading config here")
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
