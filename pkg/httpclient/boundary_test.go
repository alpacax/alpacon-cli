package httpclient

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

// repoRoot is where the walk starts, relative to this package.
const repoRoot = "../.."

var (
	// ownClients are the directories allowed to build an http.Client: this
	// package, selfupdate, which reaches GitHub with no credential and has its
	// own redirect policy, and the test helpers.
	ownClients = map[string]bool{
		"pkg/httpclient": true,
		"pkg/selfupdate": true,
		"pkg/testutil":   true,
	}

	// defaultClientUses are the net/http names that send through
	// http.DefaultClient and so follow Go's default redirect policy.
	defaultClientUses = map[string]bool{
		"DefaultClient": true,
		"Get":           true,
		"Head":          true,
		"Post":          true,
		"PostForm":      true,
	}
)

// TestNoOtherHTTPClient keeps every request on New's redirect policy: a client
// built anywhere else follows Go's default one, and so does one whose
// CheckRedirect is overwritten. It walks the AST, so an alias of net/http is
// caught and a comment is not.
func TestNoOtherHTTPClient(t *testing.T) {
	t.Parallel()
	var offenders []string
	sawClientPackage := false

	err := filepath.WalkDir(repoRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(repoRoot, path)
		if relErr != nil {
			return relErr
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if ownClients[rel] || rel == "docs" || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(rel, ".go") || strings.HasSuffix(rel, "_test.go") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return parseErr
		}
		if rel == "client/client.go" {
			sawClientPackage = true
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if assign, ok := n.(*ast.AssignStmt); ok {
				for _, lhs := range assign.Lhs {
					if sel, ok := lhs.(*ast.SelectorExpr); ok && sel.Sel.Name == "CheckRedirect" {
						offenders = append(offenders, rel+" assigns CheckRedirect")
					}
				}
			}
			return true
		})
		local, imported := netHTTPImportName(file)
		if !imported {
			return nil
		}
		if local == "." {
			// A dot import leaves Client a bare identifier no selector walk can
			// attribute, so the import itself is the offense.
			offenders = append(offenders, rel+" dot-imports net/http")
			return nil
		}
		ast.Inspect(file, func(n ast.Node) bool {
			switch node := n.(type) {
			case *ast.CompositeLit:
				if isHTTPSelector(node.Type, local, "Client") {
					offenders = append(offenders, rel+" builds an http.Client")
				}
			case *ast.CallExpr:
				if fn, ok := node.Fun.(*ast.Ident); ok && fn.Name == "new" && len(node.Args) == 1 && isHTTPSelector(node.Args[0], local, "Client") {
					offenders = append(offenders, rel+" builds an http.Client with new")
				}
			case *ast.ValueSpec:
				if isHTTPSelector(node.Type, local, "Client") {
					offenders = append(offenders, rel+" declares an http.Client value")
				}
			case *ast.SelectorExpr:
				if ident, ok := node.X.(*ast.Ident); ok && ident.Name == local && defaultClientUses[node.Sel.Name] {
					offenders = append(offenders, rel+" uses http."+node.Sel.Name)
				}
			}
			return true
		})
		return nil
	})

	require.NoError(t, err)
	// A walk that never reaches the workspace client has checked nothing.
	require.True(t, sawClientPackage, "the walk from %s never reached client/client.go", repoRoot)
	assert.Empty(t, offenders, "build the client with httpclient.New")
}

func netHTTPImportName(file *ast.File) (string, bool) {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != "net/http" {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name, true
		}
		return "http", true
	}
	return "", false
}

func isHTTPSelector(expr ast.Expr, local, name string) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	ident, ok := sel.X.(*ast.Ident)
	return ok && ident.Name == local && sel.Sel.Name == name
}
