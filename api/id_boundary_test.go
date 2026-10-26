package api

import (
	"fmt"
	"go/ast"
	"go/constant"
	"go/importer"
	"go/parser"
	"go/token"
	"go/types"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const auth0ConfigName = apiPkgPath + "/auth0.Auth0Config"

const resolveSinkField = "Endpoint"

const (
	apiPkgPath       = "github.com/alpacax/alpacon-cli/api"
	clientPkgPath    = "github.com/alpacax/alpacon-cli/client"
	utilsPkgPath     = "github.com/alpacax/alpacon-cli/utils"
	websocketPkgPath = "github.com/gorilla/websocket"
)

const (
	tunnelDialPkg     = "../pkg/tunnel/runtime"
	tunnelDialPkgPath = "github.com/alpacax/alpacon-cli/pkg/tunnel/runtime"
)

var walkedPatterns = []string{"./...", tunnelDialPkg}

var (
	buildURLName  = funcName(utils.BuildURL)
	idURLName     = funcName(IDURL)
	keepsURLName  = funcName(utils.RemovePrefixBeforeAPI)
	pathIDName    = reflect.TypeFor[PathID]().Name()
	stringName    = funcName(PathID.String)
	clientName    = reflect.TypeFor[client.AlpaconClient]().Name()
	resolveName   = reflect.TypeFor[ResolveByNameOptions]().Name()
	urlParamNames = map[string]bool{"url": true, "absoluteURL": true, "endpoint": true, "urlStr": true, "path": true}
	namedBases    = map[string]bool{"paymentBaseURL": true, "workspaceURL": true}
)

var uncheckedURL = fmt.Sprintf("a URL that is not constant, built by %s or %s, or decoded from a server response", buildURLName, idURLName)

var pathAssemblers = map[string]map[string]bool{
	"fmt":     {"Sprintf": true, "Sprint": true},
	"path":    {"Join": true},
	"net/url": {"JoinPath": true, "PathEscape": true},
	"strings": {"Join": true},
}

func TestAPILayerSendsRequestsOnlyToCheckedURLs(t *testing.T) {
	t.Parallel()
	pkgs, err := nonTestGoFiles(walkedPatterns...)
	require.NoError(t, err)
	require.Contains(t, pkgs, tunnelDialPkgPath)
	imports, err := exportDataImporter()
	require.NoError(t, err)
	var checked []checkedPackage
	decoded := map[string]bool{}
	for pkgPath, paths := range pkgs {
		pkg, err := checkPackage(pkgPath, paths, imports)
		require.NoError(t, err)
		pkg.addDecodedTypes(decoded)
		checked = append(checked, pkg)
	}

	var offenders []string
	sinks := map[string]bool{}
	for _, pkg := range checked {
		found, sent := pkg.urlOffenders(decoded)
		offenders = append(offenders, found...)
		maps.Copy(sinks, sent)
	}
	sort.Strings(offenders)

	assert.Empty(t, offenders, "build the URL with utils.BuildURL or api.IDURL, checking an ID with api.RequireID or api.RequireResponseID")
	assert.Subset(t, slices.Collect(maps.Keys(sinks)), []string{"SendGetRequest", "SendPatchRequest", "FetchAllPages", "NewRequest", "Get", "Dial", resolveName + "." + resolveSinkField})
}

func TestSinkURLParam_FindsTheURLOfEveryRequestSink(t *testing.T) {
	t.Parallel()
	imports, err := exportDataImporter()
	require.NoError(t, err)
	p, err := checkPackage("example.com/empty", nil, imports)
	require.NoError(t, err)
	clientSinks := map[string]int{
		"SendRawRequest": 1, "SendGetRequest": 0, "SendGetRequestToURL": 0, "SendGetRequestForDownload": 0,
		"SendPostRequest": 0, "SendPostRequestWithStatus": 0, "SendPatchRequest": 0, "SendMultipartStreamRequest": 0,
		"SendDeleteRequest": 0, "SendDeleteRequestWithContext": 1, "SendDeleteRequestWithBody": 0,
	}
	var sends []string
	for m := range types.NewMethodSet(types.NewPointer(p.client)).Methods() {
		if strings.HasPrefix(m.Obj().Name(), "Send") {
			sends = append(sends, m.Obj().Name())
		}
	}
	require.ElementsMatch(t, slices.Collect(maps.Keys(clientSinks)), sends)

	type sink struct {
		pkgPath, recv, name string
		want                int
	}
	sinks := []sink{
		{apiPkgPath, "", "FetchAllPages", 1}, {apiPkgPath, "", "FetchPagesUpTo", 1},
		{apiPkgPath, "", "FetchAllCursorPages", 1}, {apiPkgPath, "", "FetchCursorPages", 1},
		{"net/http", "", "NewRequest", 1}, {"net/http", "", "NewRequestWithContext", 2},
		{"net/http", "", "Get", 0}, {"net/http", "", "Head", 0}, {"net/http", "", "Post", 0}, {"net/http", "", "PostForm", 0},
		{"net/http", "Client", "Get", 0}, {"net/http", "Client", "Head", 0}, {"net/http", "Client", "Post", 0}, {"net/http", "Client", "PostForm", 0},
		{websocketPkgPath, "Dialer", "Dial", 0}, {websocketPkgPath, "Dialer", "DialContext", 1},
	}
	for name, want := range clientSinks {
		sinks = append(sinks, sink{clientPkgPath, clientName, name, want})
	}
	for _, tc := range sinks {
		pkg, err := imports.Import(tc.pkgPath) // the importer is not safe for concurrent use
		require.NoError(t, err)
		obj := pkg.Scope().Lookup(tc.name)
		if tc.recv != "" {
			obj, _, _ = types.LookupFieldOrMethod(types.NewPointer(pkg.Scope().Lookup(tc.recv).Type()), false, pkg, tc.name)
		}
		fn, ok := obj.(*types.Func)
		require.True(t, ok, "%s.%s%s is not a function", tc.pkgPath, tc.recv, tc.name)
		t.Run(tc.recv+tc.name, func(t *testing.T) {
			t.Parallel()

			i, isSink := p.sinkURLParam(fn)

			assert.True(t, isSink, "%s is not treated as a request sink", tc.name)
			assert.Equal(t, tc.want, i)
		})
	}
}

func TestURLOffenders_RefusesADotImportedBuilderPackage(t *testing.T) {
	t.Parallel()
	src := fmt.Sprintf("package fixture\n\nimport . %q\n\nvar _ = BuildURL\n", utilsPkgPath)

	offenders := fixtureOffenders(t, src)

	assertSingleOffender(t, offenders, "dot-imports a URL builder's package")
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

			offenders := fixtureOffenders(t, src)

			assertSingleOffender(t, offenders, tc.want)
		})
	}
}

const sinkFixture = `package fixture

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/gorilla/websocket"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/auth0"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const noteURL = "/api/notes/"

var (
	_ = fmt.Sprint
	_ = io.EOF
	_ = auth0.AuthEnvResponse{}
	_ = http.MethodGet
	_ = url.JoinPath
	_ = websocket.DefaultDialer
	_ = utils.BuildURL
)

type session struct {
	WebsocketURL string ` + "`json:\"websocket_url\"`" + `
	Note         string ` + "`json:\"note\"`" + `
	SkippedURL   string ` + "`json:\"-\"`" + `
}

type request struct {
	CallbackURL string ` + "`json:\"callback_url\"`" + `
}

func decode(body []byte) (session, error) {
	var s session
	return s, json.Unmarshal(body, &s)
}

func send(ac *client.AlpaconClient, id string, pathID api.PathID, resp session, req request) {
%s
}

%s
`

func TestURLOffenders_FlagsASinkSentAnUncheckedURL(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, body, extra, want string }{
		{name: "concatenation", body: `_, _ = ac.SendGetRequest(noteURL + id + "/")`, want: "hands SendGetRequest a URL"},
		{name: "sprintf", body: `_, _ = ac.SendGetRequest(fmt.Sprintf("/api/notes/%s/", id))`, want: "hands SendGetRequest a URL"},
		{name: "page_number_in_sprintf", body: `_, _ = ac.SendGetRequestToURL(fmt.Sprintf("%s?page=%d", noteURL, 2))`, want: "hands SendGetRequestToURL a URL"},
		{name: "url_join_path", body: "u, _ := url.JoinPath(noteURL, id)\n_, _ = ac.SendGetRequest(u)", want: "hands SendGetRequest a URL"},
		{name: "id_in_idurl_base", body: `_, _ = ac.SendGetRequest(api.IDURL(noteURL+id+"/", pathID, "", nil))`, want: "hands SendGetRequest a URL"},
		{name: "path_id_string_stored_in_a_variable", body: "s := pathID.String()\n_, _ = ac.SendGetRequest(noteURL + s + \"/\")", want: "hands SendGetRequest a URL"},
		{name: "pagination_helper", body: `_, _ = api.FetchAllPages[session](ac, noteURL+id, nil)`, want: "hands FetchAllPages a URL"},
		{name: "new_request", body: `_, _ = http.NewRequest(http.MethodGet, noteURL+id, nil)`, want: "hands NewRequest a URL"},
		{name: "dial_to_a_response_field_that_is_no_url", body: `_, _, _ = websocket.DefaultDialer.Dial(resp.Note, nil)`, want: "hands Dial a URL"},
		{name: "response_url_field_overwritten", body: "resp.WebsocketURL = noteURL + id\n_, _, _ = websocket.DefaultDialer.Dial(resp.WebsocketURL, nil)", want: "hands Dial a URL"},
		{name: "helper_parameter", body: `get(ac, noteURL+id)`, extra: "func get(ac *client.AlpaconClient, u string) { _, _ = ac.SendGetRequest(u) }", want: "hands SendGetRequest a URL"},
		{name: "resolve_by_name_endpoint", body: `_, _ = api.ResolveByName[session](ac, api.ResolveByNameOptions{Endpoint: noteURL + id, Name: id})`, want: "hands ResolveByNameOptions.Endpoint a URL"},
		{name: "parameter_reassigned_in_a_helper", body: `join(ac, noteURL, id)`, extra: "func join(ac *client.AlpaconClient, u, id string) { u += id; _, _ = ac.SendGetRequest(u) }", want: "hands SendGetRequest a URL"},
		{name: "parenthesized_idurl_action", body: `_, _ = ac.SendGetRequest((api.IDURL)(noteURL, pathID, id, nil))`, want: "passes IDURL a non-constant action"},
		{name: "multi_value_arguments", body: `_, _ = http.NewRequest(requestArgs())`, extra: "func requestArgs() (string, string, io.Reader) { return http.MethodGet, noteURL, nil }", want: "cannot match to its URL"},
		{name: "method_expression_unchecked", body: `_, _ = (*client.AlpaconClient).SendGetRequest(ac, noteURL+id)`, want: "hands SendGetRequest a URL"},
		{name: "named_result_overwritten_by_defer", body: `_, _ = ac.SendGetRequest(deferred(id))`, extra: "func deferred(id string) (u string) { defer func() { u = noteURL + id }(); return noteURL }", want: "hands SendGetRequest a URL"},
		{name: "request_method_through_an_interface", body: "var g getter = ac\n_, _ = g.SendGetRequest(noteURL + id)", extra: "type getter interface{ SendGetRequest(string) ([]byte, error) }", want: "hands SendGetRequest a URL"},
		{name: "helper_method_behind_an_interface", body: "fetcher{ac}.get(noteURL)\nvar g getterIface = fetcher{ac}\ng.get(noteURL + id)", extra: "type fetcher struct{ ac *client.AlpaconClient }\nfunc (f fetcher) get(u string) { _, _ = f.ac.SendGetRequest(u) }\ntype getterIface interface{ get(string) }", want: "hands SendGetRequest a URL"},
		{name: "field_tagged_json_dash", body: `_, _, _ = websocket.DefaultDialer.Dial(resp.SkippedURL, nil)`, want: "hands Dial a URL"},
		{name: "named_base_reused_as_a_local", body: "workspaceURL := noteURL + id\n_, _ = ac.SendGetRequest(utils.BuildURL(workspaceURL, \"/x/\", nil))", want: "hands SendGetRequest a URL"},
		{name: "auth0_shaped_domain_of_another_type", body: "var env struct{ Auth0 struct{ Domain string } }\nenv.Auth0.Domain = id\n_, _ = http.NewRequest(http.MethodPost, utils.BuildURL(\"https://\"+env.Auth0.Domain, \"/oauth/token\", nil), nil)", want: "hands NewRequest a URL"},
		{name: "http_client_get", body: `_, _ = ac.HTTPClient.Get(noteURL + id)`, want: "hands Get a URL"},
		{name: "sink_taken_as_a_value", body: "send := ac.SendGetRequest\n_, _ = send(noteURL + id)", want: "takes SendGetRequest as a value"},
		{name: "helper_nobody_calls", body: `_ = noteURL`, extra: "func get(ac *client.AlpaconClient, u string) { _, _ = ac.SendGetRequest(u) }", want: "hands SendGetRequest a URL"},
		{name: "exported_function_forwarding_a_url", body: `Forward(ac, noteURL)`, extra: "func Forward(ac *client.AlpaconClient, u string) { _, _ = ac.SendGetRequest(u) }", want: "hands SendGetRequest a URL"},
		{name: "function_literal_called_with_an_id", body: "fetchWith(func(u string) { _, _ = ac.HTTPClient.Get(u) }, id)", extra: "func fetchWith(fetch func(string), id string) { fetch(noteURL + id) }", want: "hands Get a URL"},
		{name: "function_literal_handed_to_an_exported_function", body: "FetchVia(func(u string) { _, _ = ac.HTTPClient.Get(u) }, resp)", extra: "func FetchVia(fetch func(string), resp session) { fetch(resp.WebsocketURL) }", want: "hands Get a URL"},
		{name: "function_parameter_with_a_constant_default", body: `withDefault(ac, nil)`, extra: "func withDefault(ac *client.AlpaconClient, get func() string) {\nif get == nil { get = func() string { return noteURL } }\n_, _ = ac.SendGetRequest(get())\n}", want: "hands SendGetRequest a URL"},
		{name: "request_struct_url_field", body: `_, _ = ac.SendGetRequestToURL(req.CallbackURL)`, want: "hands SendGetRequestToURL a URL"},
		{name: "named_base_parameter_reassigned", body: `login(ac, noteURL, id)`, extra: "func login(ac *client.AlpaconClient, workspaceURL, id string) {\nworkspaceURL += id\n_, _ = ac.SendGetRequestToURL(utils.BuildURL(workspaceURL, noteURL, nil))\n}", want: "hands SendGetRequestToURL a URL"},
		{name: "auth0_domain_overwritten", body: "var env auth0.AuthEnvResponse\nenv.Auth0.Domain = id\n_, _ = http.NewRequest(http.MethodPost, utils.BuildURL(\"https://\"+env.Auth0.Domain, \"/oauth/token\", nil), nil)", want: "hands NewRequest a URL"},
		{name: "remove_prefix_of_an_unchecked_url", body: `_, _ = ac.SendGetRequestForDownload(utils.RemovePrefixBeforeAPI(noteURL + id))`, want: "hands SendGetRequestForDownload a URL"},
		{name: "raw_request_through_an_interface_with_unnamed_parameters", body: "var r rawSender = ac\n_, _ = r.SendRawRequest(http.MethodGet, noteURL+id, nil, nil)", extra: "type rawSender interface{ SendRawRequest(string, string, io.Reader, http.Header) (*client.RawResponse, error) }", want: "hands SendRawRequest a URL"},
		{name: "request_method_the_client_does_not_have", body: "var s fooSender\n_ = s.SendFooRequest(noteURL)", extra: "type fooSender interface{ SendFooRequest(string) error }", want: "whose URL parameter this check cannot find"},
		{name: "request_url_path_changed", body: "r, _ := http.NewRequest(http.MethodGet, noteURL, nil)\nr.URL.Path = noteURL + id", want: "changes a request's URL"},
		{name: "request_url_replaced", body: "r, _ := http.NewRequest(http.MethodGet, noteURL, nil)\nr.URL, _ = url.Parse(noteURL + id)", want: "changes a request's URL"},
		{name: "request_built_as_a_literal", body: "u, _ := url.Parse(noteURL + id)\n_ = &http.Request{Method: http.MethodGet, URL: u}", want: "builds a net/http.Request literal"},
		{name: "build_url_with_a_non_constant_path", body: `_ = utils.BuildURL(noteURL, id, nil)`, want: "passes BuildURL a non-constant path"},
		{name: "path_id_concatenated", body: `_ = noteURL + pathID.String()`, want: "concatenates a PathID into a string"},
		{name: "path_id_appended", body: "s := noteURL\ns += pathID.String()\n_ = s", want: "concatenates a PathID into a string"},
		{name: "mutually_recursive_helpers", body: `_, _ = ac.SendGetRequest(first(id))`, extra: "func first(id string) string { return second(id) }\nfunc second(id string) string { return first(id) }", want: "hands SendGetRequest a URL"},
		{name: "fields_aliased_to_each_other", body: "var x, y session\nx.Note = y.Note\ny.Note = x.Note\n_, _ = ac.SendGetRequest(x.Note)", want: "hands SendGetRequest a URL"},
		{name: "http_get_function", body: `_, _ = http.Get(noteURL + id)`, want: "hands Get a URL"},
		{name: "request_url_path_taken_by_address", body: "r, _ := http.NewRequest(http.MethodGet, noteURL, nil)\np := &r.URL.Path\n*p = noteURL + id", want: "takes the address of a request's URL"},
		{name: "request_url_taken_by_address", body: "r, _ := http.NewRequest(http.MethodGet, noteURL, nil)\nchange(&r.URL)", extra: "func change(u **url.URL) {}", want: "takes the address of a request's URL"},
		{name: "constant", body: `_, _ = ac.SendGetRequest(noteURL)`},
		{name: "http_get_function_with_a_constant", body: `_, _ = http.Get(noteURL)`},
		{name: "resolve_by_name_constant_endpoint", body: `_, _ = api.ResolveByName[session](ac, api.ResolveByNameOptions{Endpoint: noteURL, Name: id})`},
		{name: "build_url_with_a_constant_path", body: `_, _ = ac.SendGetRequest(utils.BuildURL(noteURL, "search", map[string]string{"q": id}))`},
		{name: "idurl_in_a_variable", body: "u := api.IDURL(noteURL, pathID, \"close\", nil)\n_, _ = ac.SendPostRequest(u, nil)"},
		{name: "url_the_server_returned", body: `_, _, _ = websocket.DefaultDialer.Dial(resp.WebsocketURL, nil)`},
		{name: "payment_base", body: `estimate(ac, noteURL, pathID)`, extra: "func estimate(ac *client.AlpaconClient, paymentBaseURL string, pathID api.PathID) {\n_, _ = ac.SendGetRequestToURL(api.IDURL(paymentBaseURL+noteURL, pathID, \"estimate\", nil))\n}"},
		{name: "auth0_domain", body: "var env auth0.AuthEnvResponse\n_, _ = http.NewRequest(http.MethodPost, utils.BuildURL(\"https://\"+env.Auth0.Domain, \"/oauth/token\", nil), nil)"},
		{name: "method_expression", body: `_, _ = (*client.AlpaconClient).SendGetRequest(ac, noteURL)`},
		{name: "server_url_through_a_function_literal", body: "fetchVia(func(u string) { _, _ = ac.HTTPClient.Get(u) }, resp)", extra: "func fetchVia(fetch func(string), resp session) { fetch(resp.WebsocketURL) }"},
		{name: "workspace_base", body: `login(ac, id)`, extra: "func login(ac *client.AlpaconClient, workspaceURL string) {\n_, _ = ac.SendGetRequestToURL(utils.BuildURL(workspaceURL, noteURL, nil))\n}"},
		{name: "remove_prefix_of_a_server_url", body: `_, _ = ac.SendGetRequestForDownload(utils.RemovePrefixBeforeAPI(resp.WebsocketURL))`},
		{name: "raw_request_through_an_interface", body: "var r rawSender = ac\n_, _ = r.SendRawRequest(http.MethodGet, noteURL, nil, nil)", extra: "type rawSender interface{ SendRawRequest(string, string, io.Reader, http.Header) (*client.RawResponse, error) }"},
		{name: "helper_called_with_a_constant", body: `get(ac, noteURL)`, extra: "func get(ac *client.AlpaconClient, u string) { _, _ = ac.SendGetRequest(u) }"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			src := fmt.Sprintf(sinkFixture, tc.body, tc.extra)

			offenders := fixtureOffenders(t, src)

			assertSingleOffender(t, offenders, tc.want)
		})
	}
}

func assertSingleOffender(t *testing.T, offenders []string, want string) {
	t.Helper()
	if want == "" {
		assert.Empty(t, offenders)
		return
	}
	require.Len(t, offenders, 1)
	assert.Contains(t, offenders[0], want)
}

func fixtureOffenders(t *testing.T, src string) []string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "fixture.go")
	require.NoError(t, os.WriteFile(file, []byte(src), 0o600))
	imports, err := exportDataImporter()
	require.NoError(t, err)
	pkg, err := checkPackage("example.com/fixture", []string{file}, imports)
	require.NoError(t, err)
	decoded := map[string]bool{}
	pkg.addDecodedTypes(decoded)
	offenders, _ := pkg.urlOffenders(decoded)
	return offenders
}

var exportFiles = sync.OnceValues(func() (map[string]string, error) {
	args := append([]string{"list", "-export", "-deps", "-f", "{{if .Export}}{{.ImportPath}}={{.Export}}{{end}}"}, walkedPatterns...)
	out, err := exec.Command("go", args...).Output()
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
	pkg    *types.Package
	client *types.Named
	path   string
	fset   *token.FileSet
	paths  []string
	files  []*ast.File
	info   *types.Info
}

func checkPackage(pkgPath string, paths []string, imports types.Importer) (checkedPackage, error) {
	info := &types.Info{
		Types:      map[ast.Expr]types.TypeAndValue{},
		Defs:       map[*ast.Ident]types.Object{},
		Uses:       map[*ast.Ident]types.Object{},
		Selections: map[*ast.SelectorExpr]*types.Selection{},
	}
	p := checkedPackage{path: pkgPath, fset: token.NewFileSet(), paths: paths, info: info}
	for _, path := range paths {
		file, err := parser.ParseFile(p.fset, path, nil, 0)
		if err != nil {
			return p, err
		}
		p.files = append(p.files, file)
	}
	conf := types.Config{Importer: imports}
	pkg, err := conf.Check(pkgPath, p.fset, p.files, p.info)
	p.pkg = pkg
	if err != nil {
		return p, err
	}
	clientPkg, err := imports.Import(clientPkgPath)
	if err != nil {
		return p, err
	}
	p.client, _ = clientPkg.Scope().Lookup(clientName).Type().(*types.Named)
	return p, nil
}

func (p checkedPackage) addDecodedTypes(decoded map[string]bool) {
	for _, file := range p.files {
		ast.Inspect(file, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			fn := p.funcOf(calleeIdent(call))
			switch {
			case fn == nil:
			case isFunc(fn, "encoding/json", "Unmarshal") && len(call.Args) == 2:
				addDecoded(decoded, p.info.TypeOf(call.Args[1]))
			case fn.Name() == "Decode" && fn.Signature().Recv() != nil && typeName(fn.Signature().Recv().Type()) == "encoding/json.Decoder" && len(call.Args) == 1:
				addDecoded(decoded, p.info.TypeOf(call.Args[0]))
			}
			return true
		})
	}
}

func addDecoded(decoded map[string]bool, typ types.Type) {
	switch t := types.Unalias(typ).(type) {
	case *types.Pointer:
		addDecoded(decoded, t.Elem())
	case *types.Slice:
		addDecoded(decoded, t.Elem())
	case *types.Array:
		addDecoded(decoded, t.Elem())
	case *types.Map:
		addDecoded(decoded, t.Elem())
	case *types.Named:
		if name := typeName(t); name != "" && !decoded[name] {
			decoded[name] = true
			addDecoded(decoded, t.Underlying())
		}
	case *types.Struct:
		for field := range t.Fields() {
			addDecoded(decoded, field.Type())
		}
	}
}

func (p checkedPackage) resolveEndpoint() types.Object {
	apiPkg := p.pkg
	for _, imp := range p.pkg.Imports() {
		if imp.Path() == apiPkgPath {
			apiPkg = imp
		}
	}
	options := apiPkg.Scope().Lookup(resolveName)
	if apiPkg.Path() != apiPkgPath || options == nil {
		return nil
	}
	field, _, _ := types.LookupFieldOrMethod(options.Type(), false, apiPkg, resolveSinkField)
	return field
}

func (p checkedPackage) urlOffenders(decoded map[string]bool) (offenders []string, sinks map[string]bool) {
	sinks = map[string]bool{}
	report := func(pos token.Pos, format string, args ...any) {
		position := p.fset.Position(pos)
		offenders = append(offenders, fmt.Sprintf("%s:%d "+format, append([]any{repoPath(position.Filename), position.Line}, args...)...))
	}
	flow := p.urlFlow(decoded)
	for _, w := range flow.endpointWrites {
		sinks[resolveName+"."+resolveSinkField] = true
		if !flow.writeAllowed(w.write, asURL) {
			report(w.pos, "hands %s.%s %s", resolveName, resolveSinkField, uncheckedURL)
		}
	}
	for i, file := range p.files {
		utilsName, apiName := importName(file, utilsPkgPath), importName(file, apiPkgPath)
		if utilsName == "." || apiName == "." {
			offenders = append(offenders, repoPath(p.paths[i])+" dot-imports a URL builder's package")
			continue
		}
		assemblers := assemblerNames(file)
		ast.Inspect(file, func(n ast.Node) bool {
			var concatenated []ast.Expr
			switch n := n.(type) {
			case *ast.FuncDecl:
				return p.path != apiPkgPath || n.Recv != nil || n.Name.Name != idURLName
			case *ast.Ident:
				if fn := p.funcOf(n); fn != nil && !flow.callees[n] {
					if _, ok := p.sinkURLParam(fn); ok {
						report(n.Pos(), "takes %s as a value, so the URL it is called with goes unchecked", fn.Name())
					}
				}
			case *ast.BinaryExpr:
				if n.Op == token.ADD {
					concatenated = []ast.Expr{n.X, n.Y}
				}
			case *ast.AssignStmt:
				if n.Tok == token.ADD_ASSIGN {
					concatenated = n.Rhs
				}
				for _, lhs := range n.Lhs {
					if p.changesRequestURL(lhs) {
						report(lhs.Pos(), "changes a request's URL after it was built, so the URL it is sent to goes unchecked")
					}
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND && p.changesRequestURL(n.X) {
					report(n.Pos(), "takes the address of a request's URL, so the URL it is sent to goes unchecked")
				}
			case *ast.CompositeLit:
				if name := typeName(p.info.TypeOf(n)); name == "net/http.Request" || name == "net/url.URL" {
					report(n.Pos(), "builds a %s literal, so the URL it is sent to goes unchecked", name)
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
				if fn := p.funcOf(calleeIdent(n)); fn != nil {
					if i, ok := p.sinkURLParam(fn); ok {
						sinks[fn.Name()] = true
						arg := p.argument(n, i)
						switch {
						case i < 0:
							report(n.Pos(), "calls %s, whose URL parameter this check cannot find", fn.Name())
						case arg == nil:
							report(n.Pos(), "calls %s with arguments this check cannot match to its URL", fn.Name())
						case !flow.allowed(arg, asURL):
							report(arg.Pos(), "hands %s %s", fn.Name(), uncheckedURL)
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
	return offenders, sinks
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

func (p checkedPackage) argument(call *ast.CallExpr, i int) ast.Expr {
	if sel, ok := calleeExpr(call).(*ast.SelectorExpr); ok && i >= 0 {
		if s := p.info.Selections[sel]; s != nil && s.Kind() == types.MethodExpr {
			i++
		}
	}
	if i < 0 || i >= len(call.Args) {
		return nil
	}
	return call.Args[i]
}

func (p checkedPackage) funcOf(ident *ast.Ident) *types.Func {
	if ident == nil {
		return nil
	}
	fn, ok := p.info.Uses[ident].(*types.Func)
	if !ok {
		return nil
	}
	return fn.Origin()
}

func (p checkedPackage) changesRequestURL(lhs ast.Expr) bool {
	lhs = ast.Unparen(lhs)
	if sel, ok := lhs.(*ast.SelectorExpr); ok {
		s := p.info.Selections[sel]
		recv := ""
		if s != nil && s.Kind() == types.FieldVal {
			recv = typeName(s.Recv())
		}
		return recv == "net/url.URL" || recv == "net/http.Request" && sel.Sel.Name == "URL"
	}
	_, star := lhs.(*ast.StarExpr)
	return star && typeName(p.info.TypeOf(lhs)) == "net/url.URL"
}

func (p checkedPackage) clientMethod(name string) *types.Func {
	if p.client == nil {
		return nil
	}
	obj, _, _ := types.LookupFieldOrMethod(types.NewPointer(p.client), false, p.client.Obj().Pkg(), name)
	fn, _ := obj.(*types.Func)
	return fn
}

func (p checkedPackage) sinkURLParam(fn *types.Func) (int, bool) {
	sig, name := fn.Signature(), fn.Name()
	pkg := ""
	if fn.Pkg() != nil {
		pkg = fn.Pkg().Path()
	}
	recv := ""
	if sig.Recv() != nil {
		recv = typeName(sig.Recv().Type())
	}
	switch {
	case (recv == clientPkgPath+"."+clientName || sig.Recv() != nil && types.IsInterface(sig.Recv().Type())) && strings.HasPrefix(name, "Send") && strings.Contains(name, "Request"):
		method := p.clientMethod(name)
		if method == nil || !types.Identical(method.Signature().Params(), sig.Params()) {
			return -1, true
		}
		sig = method.Signature()
	case (recv == "net/http.Client" || recv == "" && pkg == "net/http") && (name == "Get" || name == "Head" || name == "Post" || name == "PostForm"):
	case recv == "" && pkg == apiPkgPath && strings.HasPrefix(name, "Fetch") && strings.Contains(name, "Pages"):
	case recv == "" && pkg == "net/http" && strings.HasPrefix(name, "NewRequest"):
	case recv == websocketPkgPath+".Dialer" && strings.HasPrefix(name, "Dial"):
	default:
		return 0, false
	}
	for i := range sig.Params().Len() {
		if urlParamNames[sig.Params().At(i).Name()] {
			return i, true
		}
	}
	return -1, true
}

type urlMode int

const (
	asURL urlMode = iota
	asBase
)

type write struct {
	expr  ast.Expr
	index int
}

type param struct {
	fn    *types.Func
	lit   *ast.FuncLit
	index int
}

type argSite struct {
	call  *ast.CallExpr
	index int
}

type positionedWrite struct {
	write
	pos token.Pos
}

type visit struct {
	obj   types.Object
	mode  urlMode
	index int
}

type urlFlowIndex struct {
	p        checkedPackage
	decoded  map[string]bool
	writes   map[types.Object][]write
	params   map[types.Object]param
	decls    map[*types.Func]*ast.FuncDecl
	calls    map[*types.Func][]*ast.CallExpr
	varCalls map[types.Object][]*ast.CallExpr
	litArgs  map[*ast.FuncLit]argSite
	paramsOf map[*types.Func][]types.Object
	passed   map[types.Object]bool
	callees  map[*ast.Ident]bool
	escaped  map[*types.Func]bool
	visiting map[visit]bool

	endpoint       types.Object
	endpointWrites []positionedWrite
}

var zeroString = &ast.BasicLit{Kind: token.STRING, Value: `""`}

func (p checkedPackage) urlFlow(decoded map[string]bool) *urlFlowIndex {
	f := &urlFlowIndex{
		p:        p,
		decoded:  decoded,
		writes:   map[types.Object][]write{},
		params:   map[types.Object]param{},
		decls:    map[*types.Func]*ast.FuncDecl{},
		calls:    map[*types.Func][]*ast.CallExpr{},
		varCalls: map[types.Object][]*ast.CallExpr{},
		litArgs:  map[*ast.FuncLit]argSite{},
		paramsOf: map[*types.Func][]types.Object{},
		passed:   map[types.Object]bool{},
		callees:  map[*ast.Ident]bool{},
		escaped:  map[*types.Func]bool{},
		visiting: map[visit]bool{},
		endpoint: p.resolveEndpoint(),
	}
	for _, file := range p.files {
		ast.Inspect(file, func(n ast.Node) bool {
			switch n := n.(type) {
			case *ast.FuncDecl:
				fn, _ := p.info.Defs[n.Name].(*types.Func)
				f.decls[fn] = n
				f.addParams(n.Type, param{fn: fn})
			case *ast.FuncLit:
				f.addParams(n.Type, param{lit: n})
			case *ast.AssignStmt:
				f.addAssign(n)
			case *ast.ValueSpec:
				for i, name := range n.Names {
					switch len(n.Values) {
					case len(n.Names):
						f.addWrite(name, write{n.Values[i], -1})
					case 1:
						f.addWrite(name, write{n.Values[0], i})
					default:
						f.addWrite(name, write{zeroString, -1})
					}
				}
			case *ast.RangeStmt:
				for _, e := range []ast.Expr{n.Key, n.Value} {
					if e != nil {
						f.addWrite(e, write{index: -1})
					}
				}
			case *ast.UnaryExpr:
				if n.Op == token.AND {
					f.addWrite(n.X, write{index: -1})
				}
			case *ast.CompositeLit:
				f.addLiteral(n)
			case *ast.CallExpr:
				if ident := calleeIdent(n); ident != nil {
					f.callees[ident] = true
					if fn := p.funcOf(ident); fn != nil {
						f.calls[fn] = append(f.calls[fn], n)
					} else if v, ok := p.info.Uses[ident].(*types.Var); ok {
						f.varCalls[v] = append(f.varCalls[v], n)
					}
				}
				for i, arg := range n.Args {
					if lit, ok := ast.Unparen(arg).(*ast.FuncLit); ok {
						f.litArgs[lit] = argSite{n, i}
					}
				}
			}
			return true
		})
	}
	for ident, obj := range p.info.Uses {
		if fn, ok := obj.(*types.Func); ok && !f.callees[ident] {
			f.escaped[fn.Origin()] = true
		}
		if v, ok := obj.(*types.Var); ok && !f.callees[ident] {
			f.passed[v] = true
		}
	}
	f.markInterfaceMethodsEscaped()
	return f
}

func (f *urlFlowIndex) markInterfaceMethodsEscaped() {
	for _, tv := range f.p.info.Types {
		if iface, ok := tv.Type.Underlying().(*types.Interface); ok && tv.IsType() {
			for m := range iface.Methods() {
				for fn := range f.decls {
					if fn != nil && fn.Signature().Recv() != nil && fn.Name() == m.Name() {
						f.escaped[fn] = true
					}
				}
			}
		}
	}
}

func (f *urlFlowIndex) addParams(ft *ast.FuncType, owner param) {
	for _, field := range ft.Params.List {
		names := field.Names
		if len(names) == 0 {
			names = []*ast.Ident{nil}
		}
		for _, name := range names {
			var obj types.Object
			if name != nil {
				obj = f.p.info.Defs[name]
			}
			if obj != nil {
				f.params[obj] = param{owner.fn, owner.lit, owner.index}
			}
			if owner.fn != nil {
				f.paramsOf[owner.fn] = append(f.paramsOf[owner.fn], obj)
			}
			owner.index++
		}
	}
}

func (f *urlFlowIndex) addAssign(n *ast.AssignStmt) {
	switch {
	case n.Tok == token.ADD_ASSIGN:
		f.addWrite(n.Lhs[0], write{&ast.BinaryExpr{X: n.Lhs[0], Op: token.ADD, Y: n.Rhs[0]}, -1})
	case n.Tok != token.ASSIGN && n.Tok != token.DEFINE:
		f.addWrite(n.Lhs[0], write{index: -1})
	case len(n.Lhs) == len(n.Rhs):
		for i, lhs := range n.Lhs {
			f.addWrite(lhs, write{n.Rhs[i], -1})
		}
	default:
		for i, lhs := range n.Lhs {
			f.addWrite(lhs, write{n.Rhs[0], i})
		}
	}
}

func (f *urlFlowIndex) addLiteral(n *ast.CompositeLit) {
	typ := f.p.info.TypeOf(n)
	if typ == nil {
		return
	}
	st, ok := deref(typ).Underlying().(*types.Struct)
	if !ok {
		return
	}
	for i, elt := range n.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			f.record(st.Field(i), write{elt, -1}, elt.Pos())
			continue
		}
		if key, ok := kv.Key.(*ast.Ident); ok {
			f.record(f.p.info.Uses[key], write{kv.Value, -1}, kv.Value.Pos())
		}
	}
}

func (f *urlFlowIndex) addWrite(lhs ast.Expr, w write) {
	var obj types.Object
	switch lhs := ast.Unparen(lhs).(type) {
	case *ast.Ident:
		obj = f.p.info.Defs[lhs]
		if obj == nil {
			obj = f.p.info.Uses[lhs]
		}
	case *ast.SelectorExpr:
		obj = f.p.info.Uses[lhs.Sel]
	}
	f.record(obj, w, lhs.Pos())
}

func (f *urlFlowIndex) record(obj types.Object, w write, pos token.Pos) {
	if obj == nil {
		return
	}
	f.writes[obj] = append(f.writes[obj], w)
	if obj == f.endpoint {
		f.endpointWrites = append(f.endpointWrites, positionedWrite{w, pos})
	}
}

func (f *urlFlowIndex) allowed(e ast.Expr, mode urlMode) bool {
	e = ast.Unparen(e)
	if f.isConstant(e) {
		return true
	}
	switch e := e.(type) {
	case *ast.CallExpr:
		return f.resultAllowed(e, 0, mode)
	case *ast.BinaryExpr:
		return mode == asBase && e.Op == token.ADD && (f.isAuth0Domain(e) || f.allowed(e.X, asBase) && f.isConstant(e.Y))
	case *ast.Ident:
		obj := f.p.info.Uses[e]
		_, isParam := f.params[obj]
		return f.varAllowed(obj, mode, isParam && mode == asBase && namedBases[e.Name])
	case *ast.SelectorExpr:
		if obj := f.p.info.Uses[e.Sel]; obj != nil && obj == f.endpoint {
			return true
		}
		sel := f.p.info.Selections[e]
		return f.varAllowed(f.p.info.Uses[e.Sel], mode, sel != nil && f.isServerURLField(sel))
	}
	return false
}

func (f *urlFlowIndex) isConstant(e ast.Expr) bool {
	_, literal := e.(*ast.BasicLit)
	return literal || f.p.info.Types[e].Value != nil
}

func (f *urlFlowIndex) isAuth0Domain(e *ast.BinaryExpr) bool {
	scheme := f.p.info.Types[e.X].Value
	domain, ok := e.Y.(*ast.SelectorExpr)
	if scheme == nil || scheme.Kind() != constant.String || constant.StringVal(scheme) != "https://" || !ok || domain.Sel.Name != "Domain" {
		return false
	}
	sel := f.p.info.Selections[domain]
	return sel != nil && typeName(sel.Recv()) == auth0ConfigName && f.varAllowed(f.p.info.Uses[domain.Sel], asBase, true)
}

func (f *urlFlowIndex) varAllowed(obj types.Object, mode urlMode, trusted bool) bool {
	v, ok := obj.(*types.Var)
	if !ok {
		return false
	}
	key := visit{v, mode, -1}
	if f.visiting[key] {
		return false
	}
	f.visiting[key] = true
	defer delete(f.visiting, key)

	writes := f.writes[v]
	prm, isParam := f.params[v]
	switch {
	case isParam && !trusted && !f.paramAllowed(prm, mode):
		return false
	case !isParam && len(writes) == 0 && !trusted:
		return false
	}
	for _, w := range writes {
		if !f.writeAllowed(w, mode) {
			return false
		}
	}
	return true
}

func (f *urlFlowIndex) writeAllowed(w write, mode urlMode) bool {
	if w.expr == nil {
		return false
	}
	if w.index < 0 {
		return f.allowed(w.expr, mode)
	}
	call, ok := ast.Unparen(w.expr).(*ast.CallExpr)
	return ok && f.resultAllowed(call, w.index, mode)
}

func (f *urlFlowIndex) paramAllowed(prm param, mode urlMode) bool {
	if prm.lit != nil {
		return f.literalHandedToHelperAllowed(prm, mode)
	}
	if prm.fn == nil {
		return false
	}
	if i, ok := f.p.sinkURLParam(prm.fn); ok && i == prm.index {
		return true
	}
	sig := prm.fn.Signature()
	calls := f.calls[prm.fn]
	if prm.fn.Exported() || f.escaped[prm.fn] || len(calls) == 0 || sig.Variadic() && prm.index == sig.Params().Len()-1 {
		return false
	}
	for _, call := range calls {
		if call.Ellipsis.IsValid() || len(call.Args) != sig.Params().Len() || !f.allowed(call.Args[prm.index], mode) {
			return false
		}
	}
	return true
}

func (f *urlFlowIndex) literalHandedToHelperAllowed(prm param, mode urlMode) bool {
	site, ok := f.litArgs[prm.lit]
	if !ok {
		return false
	}
	callee := f.p.funcOf(calleeIdent(site.call))
	if callee == nil || callee.Exported() || f.escaped[callee] || site.index >= len(f.paramsOf[callee]) {
		return false
	}
	holder := f.paramsOf[callee][site.index]
	calls := f.varCalls[holder]
	if holder == nil || f.passed[holder] || len(f.writes[holder]) > 0 || len(calls) == 0 {
		return false
	}
	for _, call := range calls {
		if prm.index >= len(call.Args) || !f.allowed(call.Args[prm.index], mode) {
			return false
		}
	}
	return true
}

func (f *urlFlowIndex) resultAllowed(call *ast.CallExpr, index int, mode urlMode) bool {
	ident := calleeIdent(call)
	if ident == nil {
		return false
	}
	if fn := f.p.funcOf(ident); fn != nil && index == 0 {
		switch {
		case isFunc(fn, utilsPkgPath, buildURLName):
			return len(call.Args) == 3 && f.isConstant(call.Args[1]) && f.allowed(call.Args[0], asBase)
		case isFunc(fn, apiPkgPath, idURLName):
			return len(call.Args) == 4 && f.allowed(call.Args[0], asBase)
		case isFunc(fn, utilsPkgPath, keepsURLName):
			return len(call.Args) == 1 && f.allowed(call.Args[0], mode)
		}
	}
	obj := f.p.info.Uses[ident]
	if fn, ok := obj.(*types.Func); ok {
		obj = fn.Origin()
	}
	key := visit{obj, mode, index}
	if f.visiting[key] {
		return false
	}
	f.visiting[key] = true
	defer delete(f.visiting, key)

	switch obj := obj.(type) {
	case *types.Func:
		decl := f.decls[obj]
		return decl != nil && f.returnsAllowed(decl.Type, decl.Body, index, mode)
	case *types.Var:
		if _, isParam := f.params[obj]; isParam {
			return false
		}
		writes := f.writes[obj]
		for _, w := range writes {
			if !f.funcValueAllowed(w, index, mode) {
				return false
			}
		}
		return len(writes) > 0
	}
	return false
}

func (f *urlFlowIndex) funcValueAllowed(w write, index int, mode urlMode) bool {
	if w.index >= 0 {
		return false
	}
	switch e := ast.Unparen(w.expr).(type) {
	case *ast.FuncLit:
		return f.returnsAllowed(e.Type, e.Body, index, mode)
	case *ast.Ident:
		fn, ok := f.p.info.Uses[e].(*types.Func)
		decl := f.decls[fn]
		return ok && decl != nil && f.returnsAllowed(decl.Type, decl.Body, index, mode)
	}
	return false
}

func (f *urlFlowIndex) returnsAllowed(ft *ast.FuncType, body *ast.BlockStmt, index int, mode urlMode) bool {
	results := ft.Results.NumFields()
	if body == nil || index >= results || len(ft.Results.List[0].Names) > 0 {
		return false // a named result can also be written by a deferred call
	}
	ok := true
	ast.Inspect(body, func(n ast.Node) bool {
		switch n := n.(type) {
		case *ast.FuncLit:
			return false
		case *ast.ReturnStmt:
			switch len(n.Results) {
			case results:
				ok = ok && f.allowed(n.Results[index], mode)
			case 1:
				call, isCall := ast.Unparen(n.Results[0]).(*ast.CallExpr)
				ok = ok && isCall && f.resultAllowed(call, index, mode)
			default:
				ok = false
			}
		}
		return ok
	})
	return ok
}

func (f *urlFlowIndex) isServerURLField(sel *types.Selection) bool {
	if sel.Kind() != types.FieldVal || !strings.HasSuffix(sel.Obj().Name(), "URL") {
		return false
	}
	typ, holder, tag := sel.Recv(), "", ""
	for _, i := range sel.Index() {
		st, ok := deref(typ).Underlying().(*types.Struct)
		if !ok {
			return false
		}
		holder = typeName(typ)
		typ, tag = st.Field(i).Type(), st.Tag(i)
	}
	name := reflect.StructTag(tag).Get("json")
	return f.decoded[holder] && name != "" && name != "-"
}

func repoPath(path string) string {
	return filepath.ToSlash(filepath.Join("api", path))
}

func calleeExpr(call *ast.CallExpr) ast.Expr {
	fun := ast.Unparen(call.Fun)
	switch x := fun.(type) {
	case *ast.IndexExpr:
		return ast.Unparen(x.X)
	case *ast.IndexListExpr:
		return ast.Unparen(x.X)
	}
	return fun
}

func calleeIdent(call *ast.CallExpr) *ast.Ident {
	switch x := calleeExpr(call).(type) {
	case *ast.Ident:
		return x
	case *ast.SelectorExpr:
		return x.Sel
	}
	return nil
}

func isFunc(fn *types.Func, pkgPath, name string) bool {
	return fn.Pkg() != nil && fn.Pkg().Path() == pkgPath && fn.Name() == name && fn.Signature().Recv() == nil
}

func deref(typ types.Type) types.Type {
	if ptr, ok := types.Unalias(typ).(*types.Pointer); ok {
		return ptr.Elem()
	}
	return typ
}

func typeName(typ types.Type) string {
	named, ok := types.Unalias(deref(typ)).(*types.Named)
	if !ok || named.Obj().Pkg() == nil {
		return ""
	}
	return named.Obj().Pkg().Path() + "." + named.Obj().Name()
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
	sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
	if !ok || pkg == "" || sel.Sel.Name != name || len(call.Args) != arity {
		return false
	}
	x, ok := sel.X.(*ast.Ident)
	return ok && x.Name == pkg
}

func isBareCall(call *ast.CallExpr, name string, arity int) bool {
	ident, ok := ast.Unparen(call.Fun).(*ast.Ident)
	return ok && ident.Name == name && len(call.Args) == arity
}
