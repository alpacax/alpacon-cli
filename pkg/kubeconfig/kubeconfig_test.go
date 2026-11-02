package kubeconfig

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
)

const (
	testName   = "alpacon-prod-east"
	testServer = "https://my-workspace.us1.example.com/kube/0b7f3c1e"
	testProxy  = "http://127.0.0.1:18443"
)

func testEntry() Entry {
	return Entry{Name: testName, Server: testServer, ProxyURL: testProxy, Token: "tok-1"}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
	require.NoError(t, os.WriteFile(path, []byte(content), 0600))
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

// decode is the semantic view of a kubeconfig, what kubectl would read.
func decode(t *testing.T, content string) map[string]any {
	t.Helper()
	var m map[string]any
	require.NoError(t, yaml.Unmarshal([]byte(content), &m))
	return m
}

func applyAndSave(t *testing.T, path string, e Entry) (string, string) {
	t.Helper()
	ed, err := Open(path)
	require.NoError(t, err)
	defer func() { assert.NoError(t, ed.Close()) }()
	prev, replaced := ed.Apply(e)
	require.NoError(t, ed.Save())
	return prev, replaced
}

func TestApplyWritesFreshFileUnderMissingDirectory(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "a", ".kube", "config")

	prev, replaced := applyAndSave(t, path, testEntry())

	assert.Empty(t, prev)
	assert.Empty(t, replaced)
	assert.Equal(t, `apiVersion: v1
clusters:
- cluster:
    proxy-url: http://127.0.0.1:18443
    server: https://my-workspace.us1.example.com/kube/0b7f3c1e
  name: alpacon-prod-east
contexts:
- context:
    cluster: alpacon-prod-east
    user: alpacon-prod-east
  name: alpacon-prod-east
current-context: alpacon-prod-east
kind: Config
preferences: {}
users:
- name: alpacon-prod-east
  user:
    token: tok-1
`, readFile(t, path))
	assert.NoFileExists(t, path+".lock")
}

func TestApplyWritesInsecureSkipTLSVerifyAsBoolean(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	e := testEntry()
	e.ProxyURL = ""
	e.InsecureSkipTLSVerify = true

	applyAndSave(t, path, e)

	got := readFile(t, path)
	assert.Contains(t, got, "- cluster:\n    insecure-skip-tls-verify: true\n    server: "+testServer+"\n")
	clusters := decode(t, got)["clusters"].([]any)
	assert.Equal(t, map[string]any{"insecure-skip-tls-verify": true, "server": testServer},
		clusters[0].(map[string]any)["cluster"])
}

// kubectl refuses a cluster that sets the insecure flag beside a CA, so the
// insecure write drops a CA an earlier entry carried and keeps everything else.
func TestApplyInsecureDropsCertificateAuthority(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, "clusters:\n- cluster:\n    certificate-authority: /etc/ca.crt\n    certificate-authority-data: Q0E=\n    extensions: []\n    server: "+testServer+"\n  name: "+testName+"\n")
	e := testEntry()
	e.ProxyURL = ""
	e.InsecureSkipTLSVerify = true

	applyAndSave(t, path, e)

	clusters := decode(t, readFile(t, path))["clusters"].([]any)
	assert.Equal(t, map[string]any{"extensions": []any{}, "insecure-skip-tls-verify": true, "server": testServer},
		clusters[0].(map[string]any)["cluster"])
}

// The TLS settings of a cluster entry belong to the endpoint they were set
// for: once the entry points at another host, keeping them would verify the
// new endpoint against the old one's CA, while the same host keeps them.
func TestApplyDropsTLSSettingsOnlyWhenTheHostChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		oldHost  string
		expected map[string]any
	}{
		{name: "same host keeps them", oldHost: "my-workspace.us1.example.com", expected: map[string]any{
			"certificate-authority": "/etc/ca.crt", "certificate-authority-data": "Q0E=", "tls-server-name": "proxy.internal",
			"extensions": []any{}, "server": testServer,
		}},
		{name: "another host drops them", oldHost: "other-workspace.us1.example.com", expected: map[string]any{
			"extensions": []any{}, "server": testServer,
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			writeFile(t, path, "clusters:\n- cluster:\n    certificate-authority: /etc/ca.crt\n    certificate-authority-data: Q0E=\n"+
				"    tls-server-name: proxy.internal\n    extensions: []\n    server: https://"+tt.oldHost+"/kube/c-prod\n  name: "+testName+"\n")
			e := testEntry()
			e.ProxyURL = ""

			applyAndSave(t, path, e)

			clusters := decode(t, readFile(t, path))["clusters"].([]any)
			assert.Equal(t, tt.expected, clusters[0].(map[string]any)["cluster"])
		})
	}
}

// A token that reads as another YAML type must still come back as a string.
func TestApplyQuotesTokenThatLooksLikeAnotherType(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	e := testEntry()
	e.Token = "12345"

	applyAndSave(t, path, e)

	users := decode(t, readFile(t, path))["users"].([]any)
	assert.Equal(t, map[string]any{"token": "12345"}, users[0].(map[string]any)["user"])
}

const kubectlFixture = `# managed by hand
apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    server: https://10.0.0.1:6443
  name: kind-dev # local kind
contexts:
- context:
    cluster: kind-dev
    namespace: default
    user: kind-dev
  name: kind-dev
current-context: kind-dev
kind: Config
preferences: {}
users:
- name: kind-dev
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
x-custom: keep-me
`

func TestApplyKeepsOtherEntriesKeysAndComments(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, kubectlFixture)

	prev, replaced := applyAndSave(t, path, testEntry())

	assert.Equal(t, "kind-dev", prev)
	assert.Empty(t, replaced)
	got := readFile(t, path)
	assert.Equal(t, decode(t, `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    server: https://10.0.0.1:6443
  name: kind-dev
- cluster:
    proxy-url: http://127.0.0.1:18443
    server: https://my-workspace.us1.example.com/kube/0b7f3c1e
  name: alpacon-prod-east
contexts:
- context:
    cluster: kind-dev
    namespace: default
    user: kind-dev
  name: kind-dev
- context:
    cluster: alpacon-prod-east
    user: alpacon-prod-east
  name: alpacon-prod-east
current-context: alpacon-prod-east
kind: Config
preferences: {}
users:
- name: kind-dev
  user:
    client-certificate-data: Q0VSVA==
    client-key-data: S0VZ
- name: alpacon-prod-east
  user:
    token: tok-1
x-custom: keep-me
`), decode(t, got))
	assert.Contains(t, got, "# managed by hand")
	assert.Contains(t, got, "# local kind")
}

const staleFixture = `apiVersion: v1
clusters:
- cluster:
    certificate-authority-data: Q0E=
    insecure-skip-tls-verify: true
    proxy-url: http://127.0.0.1:9999
    server: https://other-workspace.ap1.example.com/kube/0b7f3c1e
  name: alpacon-prod-east
- cluster:
    server: https://duplicate.example.com
  name: alpacon-prod-east
contexts:
- context:
    cluster: old-cluster
    extensions:
    - extension: {}
      name: my-extension
    namespace: team-a
    user: old-user
  name: alpacon-prod-east
- context:
    cluster: duplicate
  name: alpacon-prod-east
current-context: alpacon-prod-east
kind: Config
preferences: {}
users:
- name: alpacon-prod-east
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      command: stale-plugin
    token: stale
`

func TestApplyReplacesEntriesInPlace(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, staleFixture)
	e := testEntry()
	e.ProxyURL = ""

	prev, replaced := applyAndSave(t, path, e)

	assert.Equal(t, testName, prev)
	assert.Equal(t, "other-workspace.ap1.example.com", replaced)
	// The old endpoint's CA leaves with it; see the TLS settings test.
	assert.Equal(t, decode(t, `apiVersion: v1
clusters:
- cluster:
    server: https://my-workspace.us1.example.com/kube/0b7f3c1e
  name: alpacon-prod-east
contexts:
- context:
    cluster: alpacon-prod-east
    extensions:
    - extension: {}
      name: my-extension
    namespace: team-a
    user: alpacon-prod-east
  name: alpacon-prod-east
current-context: alpacon-prod-east
kind: Config
preferences: {}
users:
- name: alpacon-prod-east
  user:
    token: tok-1
`), decode(t, readFile(t, path)))
}

func TestApplyRerunIsIdempotent(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, kubectlFixture)

	applyAndSave(t, path, testEntry())
	first := readFile(t, path)
	prev, replaced := applyAndSave(t, path, testEntry())

	assert.Equal(t, testName, prev)
	assert.Empty(t, replaced)
	assert.Equal(t, first, readFile(t, path))
}

func TestApplyReportsReplacedServerHost(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		oldServer string
		want      string
	}{
		{name: "same host other path", oldServer: "https://my-workspace.us1.example.com/kube/other", want: ""},
		{name: "same host other case", oldServer: "https://MY-WORKSPACE.us1.example.com/kube/0b7f3c1e", want: ""},
		{name: "other host", oldServer: "https://other.ap1.example.com/kube/0b7f3c1e", want: "other.ap1.example.com"},
		{name: "other port", oldServer: "https://my-workspace.us1.example.com:8443/kube/0b7f3c1e", want: "my-workspace.us1.example.com:8443"},
		{name: "unparsable old value", oldServer: "not a url", want: "not a url"},
		{name: "no old server", oldServer: "", want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			writeFile(t, path, "clusters:\n- cluster:\n    server: \""+tc.oldServer+"\"\n  name: "+testName+"\n")

			_, replaced := applyAndSave(t, path, testEntry())

			assert.Equal(t, tc.want, replaced)
		})
	}
}

func TestApplyTurnsFlowListIntoBlockList(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, "apiVersion: v1\nclusters: [] # none yet\ncontexts: []\nkind: Config\nusers: []\n")

	applyAndSave(t, path, testEntry())

	got := readFile(t, path)
	assert.Contains(t, got, "clusters: # none yet\n- cluster:\n")
	assert.Contains(t, got, "contexts:\n- context:\n")
	assert.Contains(t, got, "users:\n- name: "+testName+"\n")
	assert.NotContains(t, got, "[]")
}

// Shapes that hold no entries yet still open as a config to fill.
func TestOpenAcceptsEmptyShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
		keep    string
	}{
		{name: "empty file", content: ""},
		{name: "comments only", content: "# nothing yet\n\n  # second line\r\n", keep: "# nothing yet\n# second line\n"},
		{name: "document marker only", content: "---\n"},
		{name: "explicit null", content: "null\n"},
		{name: "trailing empty document", content: "apiVersion: v1\n---\n"},
		{name: "empty lists", content: "clusters:\ncontexts:\nusers:\ncurrent-context:\n"},
		{name: "unknown keys only", content: "x-custom: keep-me\n", keep: "x-custom: keep-me"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			writeFile(t, path, tc.content)

			applyAndSave(t, path, testEntry())

			server, proxyURL, err := Lookup([]string{path}, testName)
			require.NoError(t, err)
			assert.Equal(t, testServer, server)
			assert.Equal(t, testProxy, proxyURL)
			if tc.keep != "" {
				assert.Contains(t, readFile(t, path), tc.keep)
			}
		})
	}
}

func TestOpenRefusesUnsupportedShapes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		content string
	}{
		{name: "invalid YAML", content: "clusters: [\n"},
		{name: "top level is a list", content: "- apiVersion: v1\n"},
		{name: "top level is a string", content: "hello\n"},
		{name: "two documents", content: "apiVersion: v1\n---\napiVersion: v1\n"},
		{name: "clusters is a mapping", content: "clusters:\n  name: x\n"},
		{name: "users is a string", content: "users: nobody\n"},
		{name: "contexts item is a string", content: "contexts:\n- my-context\n"},
		{name: "clusters item is an alias", content: "base: &b\n  name: x\nclusters:\n- *b\n"},
		{name: "users item holds an anchor", content: "users:\n- &u\n  name: x\n"},
		{name: "cluster value is an alias", content: "ca: &ca\n  server: https://x\nclusters:\n- cluster: *ca\n  name: x\n"},
		{name: "cluster is a string", content: "clusters:\n- cluster: https://x\n  name: x\n"},
		{name: "item name is a mapping", content: "clusters:\n- name:\n    a: b\n"},
		{name: "duplicate top level key", content: "clusters: []\nclusters: []\n"},
		{name: "duplicate item key", content: "users:\n- name: a\n  name: b\n"},
		{name: "top level merge key", content: "base: &b\n  kind: Config\n<<: *b\n"},
		{name: "current-context is a mapping", content: "current-context:\n  a: b\n"},
		{name: "current-context is an alias", content: "n: &n x\ncurrent-context: *n\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			path := filepath.Join(t.TempDir(), "config")
			writeFile(t, path, tc.content)

			ed, err := Open(path)

			require.ErrorIs(t, err, ErrMalformed)
			assert.Nil(t, ed)
			assert.Contains(t, err.Error(), path)
			assert.Equal(t, tc.content, readFile(t, path))
			assert.NoFileExists(t, path+".lock")
		})
	}
}

func TestOpenRefusesHeldLock(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, kubectlFixture)
	writeFile(t, path+".lock", "")

	ed, err := Open(path)

	require.ErrorIs(t, err, ErrLocked)
	assert.Nil(t, ed)
	assert.Equal(t, "another process is editing "+path+"; remove "+path+".lock if no kubectl is running", err.Error())
	assert.Equal(t, kubectlFixture, readFile(t, path))
	assert.FileExists(t, path+".lock")
}

func TestOpenHoldsLockUntilClose(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")

	ed, err := Open(path)
	require.NoError(t, err)
	assert.FileExists(t, path+".lock")
	_, err = Open(path)
	require.ErrorIs(t, err, ErrLocked)

	require.NoError(t, ed.Close())
	assert.NoFileExists(t, path+".lock")
	require.NoError(t, ed.Close())
	require.Error(t, ed.Save())
	assert.NoFileExists(t, path)
}

func TestOpenRefusesDirectory(t *testing.T) {
	t.Parallel()
	path := t.TempDir()

	_, err := Open(path)

	require.Error(t, err)
	assert.Contains(t, err.Error(), "is a directory")
	assert.NoFileExists(t, path+".lock")
}

func TestOpenRefusesEmptyPath(t *testing.T) {
	t.Parallel()
	_, err := Open("")
	require.Error(t, err)
}

const lookupFixture = `apiVersion: v1
clusters:
- cluster:
    proxy-url: http://127.0.0.1:18443
    server: https://my-workspace.us1.example.com/kube/0b7f3c1e
  name: prod-cluster
- cluster:
    server: https://direct.example.com
  name: direct
- cluster:
    proxy-url: http://127.0.0.1:18443
  name: no-server
contexts:
- context:
    cluster: prod-cluster
    user: u
  name: alpacon-prod-east
- context:
    cluster: direct
  name: alpacon-direct
- context:
    cluster: gone
  name: alpacon-gone
- context:
    user: u
  name: alpacon-no-cluster
- context:
    cluster: direct
  name: alpacon-twice
- context:
    cluster: direct
  name: alpacon-twice
- context:
    cluster: no-server
  name: alpacon-no-server
kind: Config
`

func TestLookup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "config")
	writeFile(t, path, lookupFixture)

	cases := []struct {
		name      string
		path      string
		context   string
		wantSrv   string
		wantProxy string
		wantErr   error
		wantMsg   string
	}{
		{name: "hit with proxy", path: path, context: "alpacon-prod-east", wantSrv: testServer, wantProxy: testProxy},
		{name: "hit without proxy", path: path, context: "alpacon-direct", wantSrv: "https://direct.example.com"},
		{name: "missing context", path: path, context: "alpacon-absent", wantErr: ErrContextNotFound},
		{name: "missing file", path: filepath.Join(dir, "absent"), context: "alpacon-prod-east", wantErr: ErrContextNotFound},
		{name: "missing cluster", path: path, context: "alpacon-gone", wantErr: ErrClusterNotFound},
		{name: "context names no cluster", path: path, context: "alpacon-no-cluster", wantErr: ErrClusterNotFound},
		{name: "duplicate context", path: path, context: "alpacon-twice", wantErr: ErrMalformed},
		{name: "cluster without server", path: path, context: "alpacon-no-server", wantMsg: `cluster "no-server" in ` + path + " has no server"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server, proxyURL, err := Lookup([]string{tc.path}, tc.context)
			if tc.wantMsg != "" {
				require.EqualError(t, err, tc.wantMsg)
				assert.NotErrorIs(t, err, ErrClusterNotFound)
				return
			}
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				assert.Contains(t, err.Error(), tc.path)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSrv, server)
			assert.Equal(t, tc.wantProxy, proxyURL)
		})
	}
}

// kubectl merges every file it is given, taking each name from the first file
// that defines it; Lookup has to find what kubectl would use.
func TestLookupMergesFilesLikeKubectl(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	write := func(name, content string) string {
		path := filepath.Join(dir, name)
		writeFile(t, path, content)
		return path
	}
	unrelated := write("unrelated", "contexts:\n- context:\n    cluster: other\n  name: other\nclusters:\n- cluster:\n    server: https://other.example.com\n  name: other\n")
	contextOnly := write("context-only", "contexts:\n- context:\n    cluster: split\n  name: alpacon-split\n")
	clusterOnly := write("cluster-only", "clusters:\n- cluster:\n    server: https://split.example.com\n  name: split\n")
	early := write("early", "contexts:\n- context:\n    cluster: early\n  name: alpacon-both\nclusters:\n- cluster:\n    server: https://early.example.com\n  name: early\n")
	late := write("late", "contexts:\n- context:\n    cluster: late\n  name: alpacon-both\nclusters:\n- cluster:\n    server: https://late.example.com\n  name: late\n")
	malformed := write("malformed", "contexts: nope\n")
	missing := filepath.Join(dir, "missing")

	cases := []struct {
		name    string
		paths   []string
		context string
		wantSrv string
		wantErr error
	}{
		{name: "context in a later file", paths: []string{unrelated, early}, context: "alpacon-both", wantSrv: "https://early.example.com"},
		{name: "context and cluster in different files", paths: []string{contextOnly, unrelated, clusterOnly}, context: "alpacon-split", wantSrv: "https://split.example.com"},
		{name: "first file wins", paths: []string{late, early}, context: "alpacon-both", wantSrv: "https://late.example.com"},
		{name: "missing file skipped", paths: []string{missing, early}, context: "alpacon-both", wantSrv: "https://early.example.com"},
		{name: "malformed file after a hit still fails", paths: []string{early, malformed}, context: "alpacon-both", wantErr: ErrMalformed},
		{name: "found nowhere", paths: []string{unrelated, missing}, context: "alpacon-both", wantErr: ErrContextNotFound},
		{name: "cluster found nowhere", paths: []string{contextOnly, unrelated}, context: "alpacon-split", wantErr: ErrClusterNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			server, _, err := Lookup(tc.paths, tc.context)
			if tc.wantErr != nil {
				require.ErrorIs(t, err, tc.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.wantSrv, server)
		})
	}
}

func TestLookupRefusesMalformedFile(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, "contexts: nope\n")

	_, _, err := Lookup([]string{path}, testName)

	require.ErrorIs(t, err, ErrMalformed)
}

// Lookup reads what Apply wrote, which kube proxy relies on.
func TestLookupReadsAppliedEntry(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	writeFile(t, path, kubectlFixture)
	applyAndSave(t, path, testEntry())

	server, proxyURL, err := Lookup([]string{path}, testName)

	require.NoError(t, err)
	assert.Equal(t, testServer, server)
	assert.Equal(t, testProxy, proxyURL)
}

func execEntry(cluster string) Entry {
	return Entry{
		Name:   "alpacon-" + cluster,
		Server: testServer,
		Exec:   &ExecUser{Command: "alpacon", Args: []string{"kube", "credentials", "--cluster", cluster}},
	}
}

func TestApplyWritesAnExecUser(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")

	applyAndSave(t, path, execEntry("prod-east"))

	assert.Equal(t, `apiVersion: v1
clusters:
- cluster:
    server: https://my-workspace.us1.example.com/kube/0b7f3c1e
  name: alpacon-prod-east
contexts:
- context:
    cluster: alpacon-prod-east
    user: alpacon-prod-east
  name: alpacon-prod-east
current-context: alpacon-prod-east
kind: Config
preferences: {}
users:
- name: alpacon-prod-east
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1
      args:
      - kube
      - credentials
      - --cluster
      - prod-east
      command: alpacon
      interactiveMode: Never
      provideClusterInfo: true
`, readFile(t, path))
}

// A cluster name that reads as another YAML type still reaches the plugin as
// the string it is.
func TestApplyQuotesExecArgsThatLookLikeAnotherType(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")

	applyAndSave(t, path, execEntry("123"))

	users := decode(t, readFile(t, path))["users"].([]any)
	user := users[0].(map[string]any)["user"].(map[string]any)
	exec := user["exec"].(map[string]any)
	assert.Equal(t, map[string]any{
		"apiVersion":         "client.authentication.k8s.io/v1",
		"args":               []any{"kube", "credentials", "--cluster", "123"},
		"command":            "alpacon",
		"interactiveMode":    "Never",
		"provideClusterInfo": true,
	}, exec)
}

// The exec user replaces the token a static context left, and the rerun of
// the same entry rewrites nothing.
func TestApplyExecReplacesATokenUser(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	applyAndSave(t, path, Entry{Name: "alpacon-prod-east", Server: testServer, Token: "tok-1"})

	applyAndSave(t, path, execEntry("prod-east"))
	first := readFile(t, path)
	applyAndSave(t, path, execEntry("prod-east"))

	assert.Equal(t, first, readFile(t, path))
	users := decode(t, first)["users"].([]any)
	require.Len(t, users, 1)
	user := users[0].(map[string]any)["user"].(map[string]any)
	assert.NotContains(t, user, "token")
	assert.Contains(t, user, "exec")
}
