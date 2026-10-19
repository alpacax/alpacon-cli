package api

import (
	"fmt"
	"go/ast"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
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

	imports, err := exportDataImporter()
	require.NoError(t, err)
	var offenders []string
	for dir, paths := range dirs {
		pkgPath := apiPkgPath
		if dir != "." {
			pkgPath += "/" + filepath.ToSlash(dir)
		}
		pkg, err := checkPackage(pkgPath, paths, imports)
		require.NoError(t, err)
		offenders = append(offenders, pkg.urlOffenders()...)
	}
	sort.Strings(offenders)

	assert.Empty(t, offenders, "check the ID with api.RequireID or api.RequireResponseID and build the URL with api.IDURL")
}

func TestURLOffenders_FlagsAPathIDHandedToAPathAssembler(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, imp, expr, want string }{
		{name: "value_to_sprintf", imp: "fmt", expr: `fmt.Sprintf("/api/notes/%s/", pathID)`, want: "hands a PathID to fmt.Sprintf"},
		{name: "pointer_to_sprint", imp: "fmt", expr: `fmt.Sprint(&pathID)`, want: "hands a PathID to fmt.Sprint"},
		{name: "string_call_to_path_join", imp: "path", expr: `path.Join("/api/notes", pathID.String())`, want: "hands a PathID to path.Join"},
		{name: "checked_idurl", expr: `api.IDURL("/api/notes/", pathID, "", nil)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			imp := ""
			if tc.imp != "" {
				imp = fmt.Sprintf("%q\n", tc.imp)
			}
			src := fmt.Sprintf("package fixture\n\nimport (\n%s%q\n)\n\nfunc build(pathID api.PathID) string { return %s }\n", imp, apiPkgPath, tc.expr)
			file := filepath.Join(t.TempDir(), "fixture.go")
			require.NoError(t, os.WriteFile(file, []byte(src), 0o600))

			imports, err := exportDataImporter()
			require.NoError(t, err)
			pkg, err := checkPackage("example.com/fixture", []string{file}, imports)
			require.NoError(t, err)
			offenders := pkg.urlOffenders()

			if tc.want == "" {
				assert.Empty(t, offenders)
				return
			}
			require.Len(t, offenders, 1)
			assert.Contains(t, offenders[0], tc.want)
		})
	}
}

var exportFiles = sync.OnceValues(func() (map[string]string, error) {
	out, err := exec.Command("go", "list", "-export", "-deps", "-f", "{{if .Export}}{{.ImportPath}}={{.Export}}{{end}}", "./...").Output()
	if err != nil {
		return nil, err
	}
	exports := map[string]string{}
	for line := range strings.Lines(string(out)) {
		if pkgPath, file, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			exports[pkgPath] = file
		}
	}
	return exports, nil
})

func exportDataImporter() (types.Importer, error) {
	exports, err := exportFiles()
	if err != nil {
		return nil, err
	}
	return importer.ForCompiler(token.NewFileSet(), "gc", func(pkgPath string) (io.ReadCloser, error) {
		file, ok := exports[pkgPath]
		if !ok {
			return nil, fmt.Errorf("no export data for %s", pkgPath)
		}
		return os.Open(file)
	}), nil
}

type checkedPackage struct {
	path  string
	fset  *token.FileSet
	paths []string
	files []*ast.File
	info  *types.Info
}

func checkPackage(pkgPath string, paths []string, imports types.Importer) (checkedPackage, error) {
	p := checkedPackage{path: pkgPath, fset: token.NewFileSet(), paths: paths, info: &types.Info{Types: map[ast.Expr]types.TypeAndValue{}}}
	for _, path := range paths {
		file, err := parser.ParseFile(p.fset, path, nil, 0)
		if err != nil {
			return p, err
		}
		p.files = append(p.files, file)
	}
	conf := types.Config{Importer: imports}
	_, err := conf.Check(pkgPath, p.fset, p.files, p.info)
	return p, err
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
	if call, ok := expr.(*ast.CallExpr); ok && len(call.Args) == 0 {
		if sel, ok := call.Fun.(*ast.SelectorExpr); ok && sel.Sel.Name == stringName {
			expr = sel.X
		}
	}
	typ := types.Unalias(p.info.Types[expr].Type)
	if ptr, ok := typ.(*types.Pointer); ok {
		typ = types.Unalias(ptr.Elem())
	}
	named, ok := typ.(*types.Named)
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
