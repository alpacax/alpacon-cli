package api

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alpacax/alpacon-cli/utils"
)

const (
	apiPkgPath   = "github.com/alpacax/alpacon-cli/api"
	utilsPkgPath = "github.com/alpacax/alpacon-cli/utils"
)

var (
	buildURLName = funcName(utils.BuildURL)
	idURLName    = funcName(IDURL)
	pathIDName   = reflect.TypeFor[PathID]().Name()
	stringName   = funcName(PathID.String)
)

var pathAssemblers = map[string]map[string]bool{
	"fmt":     {"Sprintf": true, "Sprint": true},
	"path":    {"Join": true},
	"net/url": {"JoinPath": true, "PathEscape": true},
	"strings": {"Join": true},
}

func TestAPILayerBuildsIDPathsFromCheckedSegments(t *testing.T) {
	t.Parallel()
	dirs, err := nonTestGoFiles()
	require.NoError(t, err)

	root, err := checkPackage(apiPkgPath, dirs["."], apiOnlyImporter{})
	require.NoError(t, err)
	offenders := root.urlOffenders()
	for dir, paths := range dirs {
		if dir == "." {
			continue
		}
		pkg, err := checkPackage(apiPkgPath+"/"+filepath.ToSlash(dir), paths, apiOnlyImporter{api: root.types})
		require.NoError(t, err)
		offenders = append(offenders, pkg.urlOffenders()...)
	}
	sort.Strings(offenders)

	assert.Empty(t, offenders, "check the ID with api.RequireID or api.RequireResponseID and build the URL with api.IDURL")
}

type apiOnlyImporter struct{ api *types.Package }

func (s apiOnlyImporter) Import(path string) (*types.Package, error) {
	if path == apiPkgPath {
		return s.api, nil
	}
	pkg := types.NewPackage(path, filepath.Base(path))
	pkg.MarkComplete()
	return pkg, nil
}

type checkedPackage struct {
	path  string
	fset  *token.FileSet
	paths []string
	files []*ast.File
	info  *types.Info
	types *types.Package
}

func checkPackage(pkgPath string, paths []string, importer types.Importer) (checkedPackage, error) {
	p := checkedPackage{path: pkgPath, fset: token.NewFileSet(), paths: paths, info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}}
	for _, path := range paths {
		file, err := parser.ParseFile(p.fset, path, nil, 0)
		if err != nil {
			return p, err
		}
		p.files = append(p.files, file)
	}
	conf := types.Config{Importer: importer, Error: func(error) {}}
	p.types, _ = conf.Check(pkgPath, p.fset, p.files, p.info)
	return p, nil
}

func (p checkedPackage) urlOffenders() []string {
	var offenders []string
	for i, file := range p.files {
		repoPath := "api/" + p.paths[i]
		report := func(pos token.Pos, format string, args ...any) {
			offenders = append(offenders, fmt.Sprintf("%s:%d "+format, append([]any{repoPath, p.fset.Position(pos).Line}, args...)...))
		}
		utilsName, apiName := importName(file, utilsPkgPath), importName(file, apiPkgPath)
		if utilsName == "." || apiName == "." {
			offenders = append(offenders, repoPath+" dot-imports a URL builder's package")
			continue
		}
		assemblers := assemblerNames(file)
		ast.Inspect(file, func(n ast.Node) bool {
			var concatenated []ast.Expr
			switch n := n.(type) {
			case *ast.FuncDecl:
				return p.path != apiPkgPath || n.Recv != nil || n.Name.Name != idURLName
			case *ast.BinaryExpr:
				if n.Op == token.ADD {
					concatenated = []ast.Expr{n.X, n.Y}
				}
			case *ast.AssignStmt:
				if n.Tok == token.ADD_ASSIGN {
					concatenated = n.Rhs
				}
			case *ast.CallExpr:
				if sel, ok := n.Fun.(*ast.SelectorExpr); ok {
					if x, ok := sel.X.(*ast.Ident); ok && assemblers[x.Name][sel.Sel.Name] {
						for _, arg := range n.Args {
							if p.isPathIDString(arg) {
								report(arg.Pos(), "hands a PathID to %s.%s", x.Name, sel.Sel.Name)
							}
						}
					}
				}
				switch {
				case isCall(n, utilsName, buildURLName, 3):
					if p.info.Types[n.Args[1]].Value == nil {
						report(n.Pos(), "passes %s a non-constant path", buildURLName)
					}
				case isCall(n, apiName, idURLName, 4) || (p.path == apiPkgPath && isBareCall(n, idURLName, 4)):
					if p.info.Types[n.Args[2]].Value == nil {
						report(n.Pos(), "passes %s a non-constant action", idURLName)
					}
				}
			}
			for _, operand := range concatenated {
				if p.isPathIDString(operand) {
					report(operand.Pos(), "concatenates a PathID into a string")
				}
			}
			return true
		})
	}
	return offenders
}

func (p checkedPackage) isPathIDString(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 0 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != stringName {
		return false
	}
	named, ok := types.Unalias(p.info.Types[sel.X].Type).(*types.Named)
	return ok && named.Obj().Name() == pathIDName && named.Obj().Pkg() != nil && named.Obj().Pkg().Path() == apiPkgPath
}

func assemblerNames(file *ast.File) map[string]map[string]bool {
	names := map[string]map[string]bool{}
	for pkgPath, funcs := range pathAssemblers {
		if name := importName(file, pkgPath); name != "" {
			names[name] = funcs
		}
	}
	return names
}

func funcName(fn any) string {
	full := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()).Name()
	return full[strings.LastIndex(full, ".")+1:]
}

func isCall(call *ast.CallExpr, pkg, name string, arity int) bool {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || pkg == "" || sel.Sel.Name != name || len(call.Args) != arity {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg
}

func isBareCall(call *ast.CallExpr, name string, arity int) bool {
	ident, ok := call.Fun.(*ast.Ident)
	return ok && ident.Name == name && len(call.Args) == arity
}
