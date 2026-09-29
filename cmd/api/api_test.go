package apicmd

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/pkg/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type apiRoundTripFunc func(*http.Request) (*http.Response, error)

func (f apiRoundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) { return f(req) }

func apiTestClient(fn apiRoundTripFunc) *client.AlpaconClient {
	ac := &client.AlpaconClient{BaseURL: "https://workspace.example", HTTPClient: &http.Client{Transport: fn}, UserAgent: "alpacon-test"}
	ac.SetAccessToken("secret-token")
	return ac
}

func apiTestResponse(status int, contentType, body string) *http.Response {
	return &http.Response{Status: strings.TrimSpace(strings.Join([]string{strconv.Itoa(status), http.StatusText(status)}, " ")), StatusCode: status, Proto: "HTTP/1.1", Header: http.Header{"Content-Type": []string{contentType}, "X-Trace": []string{"trace-1"}}, Body: io.NopCloser(strings.NewReader(body))}
}

// runAPITest chains prepareRequest and runAPI the way the command's Run does,
// so tests written against the pre-split runAPI signature keep working.
func runAPITest(ac *client.AlpaconClient, opts options, stdout, stderr io.Writer, stdin io.Reader) (int, error) {
	request, code, err := prepareRequest(opts, stdin)
	if err != nil {
		return code, err
	}
	return runAPI(ac, opts, request, stdout, stderr)
}

func TestRunAPI_MethodAndFieldRouting(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		opts       options
		wantMethod string
		wantQuery  string
		wantBody   string
	}{
		{"default get", options{Endpoint: "api/v1/x"}, "GET", "", ""},
		{"field post", options{Endpoint: "/api/v1/x", Fields: []fieldInput{{"name=one", false}}}, "POST", "", `{"name":"one"}`},
		{"explicit get", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"name=one", false}}}, "GET", "name=one", ""},
		{"head query", options{Endpoint: "/api/v1/x", Method: "HEAD", MethodSet: true, Fields: []fieldInput{{"n=2", true}}}, "HEAD", "n=2", ""},
		{"input post", options{Endpoint: "/api/v1/x", Input: "-"}, "POST", "", "input"},
		{"input with query", options{Endpoint: "/api/v1/x", Input: "-", Fields: []fieldInput{{"n=2", true}}}, "POST", "n=2", "input"},
		{"existing query preserved", options{Endpoint: "/api/v1/x?q=hello%20world"}, "GET", "q=hello%20world", ""},
		{"existing query with field", options{Endpoint: "/api/v1/x?q=hello%20world", Method: "GET", MethodSet: true, Fields: []fieldInput{{"n=2", true}}}, "GET", "q=hello%20world&n=2", ""},
		{"gh nested query", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"filter[name]=one", false}}}, "GET", "filter%5Bname%5D=one", ""},
		{"gh null query", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"none=null", true}}}, "GET", "none=", ""},
		{"gh empty array query", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"names[]", false}}}, "GET", "", ""},
		{"gh array query", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"names[]=one", false}, {"names[]=two", false}}}, "GET", "names%5B%5D=one&names%5B%5D=two", ""},
		{"nested map query preserves parent", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"filter[name]=demo", false}}}, "GET", "filter%5Bname%5D=demo", ""},
		{"nested array query preserves parent", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"tags[]=one", false}, {"tags[]=two", false}}}, "GET", "tags%5B%5D=one&tags%5B%5D=two", ""},
		{"nested array of objects query preserves parent", options{Endpoint: "/api/v1/x", Method: "GET", MethodSet: true, Fields: []fieldInput{{"items[][name]=one", false}}}, "GET", "items%5B%5D%5Bname%5D=one", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				assert.Equal(t, tc.wantMethod, r.Method)
				assert.Equal(t, "/api/v1/x", r.URL.Path)
				assert.Equal(t, tc.wantQuery, r.URL.RawQuery)
				var body []byte
				if r.Body != nil {
					body, _ = io.ReadAll(r.Body)
				}
				if strings.HasPrefix(tc.wantBody, "{") {
					assert.JSONEq(t, tc.wantBody, string(body))
				} else {
					assert.Equal(t, tc.wantBody, string(body))
				}
				return apiTestResponse(http.StatusNoContent, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, tc.opts, &stdout, &stderr, strings.NewReader("input"))

			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, 1, requests)
		})
	}
}

func TestRunAPI_HeaderOverridesDefaultContentType(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name string
		want string
	}{
		{"nonempty", "text/plain"},
		{"empty", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				assert.Equal(t, tc.want, r.Header.Get("Content-Type"))
				return apiTestResponse(http.StatusNoContent, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Fields: []fieldInput{{"name=one", false}}, Headers: []string{"Content-Type: " + tc.want}}, &stdout, &stderr, strings.NewReader(""))

			require.NoError(t, err)
			assert.Equal(t, 0, code)
		})
	}
}

func TestRunAPI_HeaderOverridesDefaultContentTypeWithInputBody(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "text/csv", r.Header.Get("Content-Type"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "a,b\n1,2\n", string(body))
		return apiTestResponse(http.StatusNoContent, "", ""), nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Input: "-", Headers: []string{"Content-Type: text/csv"}}, &stdout, &stderr, strings.NewReader("a,b\n1,2\n"))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
}

func TestRunAPI_HeaderOutputStripsTerminalControls(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		response := apiTestResponse(http.StatusOK, "text/plain", "body")
		response.Header.Set("X-Trace", "\x1b[31mtrace\x1b[0m")
		return response, nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Include: true, Verbose: true, Headers: []string{"X-Request: safe"}}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.NotContains(t, stdout.String(), "\x1b")
	assert.NotContains(t, stderr.String(), "\x1b")
}

func TestRunAPI_VerboseRedactsSensitiveRequestAndResponseHeaders(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		response := apiTestResponse(http.StatusOK, "text/plain", "body")
		response.Header.Set("Set-Cookie", "session=response-secret")
		response.Header.Set("Proxy-Authorization", "Basic response-proxy-secret")
		response.Header.Set("Cookie", "session=response-cookie-secret")
		return response, nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Verbose: true, Headers: []string{
		"Proxy-Authorization: Basic proxy-secret",
		"Cookie: session=request-secret",
		"Set-Cookie: session=request-cookie-secret",
	}}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "Proxy-Authorization: [REDACTED]\n")
	assert.Contains(t, stderr.String(), "Cookie: [REDACTED]\n")
	assert.Contains(t, stderr.String(), "Set-Cookie: [REDACTED]\n")
	assert.NotContains(t, stderr.String(), "proxy-secret")
	assert.NotContains(t, stderr.String(), "request-secret")
	assert.NotContains(t, stderr.String(), "request-cookie-secret")
	assert.NotContains(t, stderr.String(), "response-secret")
	assert.NotContains(t, stderr.String(), "response-proxy-secret")
	assert.NotContains(t, stderr.String(), "response-cookie-secret")
}

func TestRunAPI_IncludeRedactsSensitiveResponseHeader(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		response := apiTestResponse(http.StatusOK, "text/plain", "body")
		response.Header.Set("Set-Cookie", "session=response-secret")
		response.Header.Set("Authorization", "Bearer response-auth-secret")
		response.Header.Set("Proxy-Authorization", "Basic response-proxy-secret")
		response.Header.Set("Cookie", "session=response-cookie-secret")
		return response, nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Include: true}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stdout.String(), "Set-Cookie: [REDACTED]\n")
	assert.Contains(t, stdout.String(), "Authorization: [REDACTED]\n")
	assert.Contains(t, stdout.String(), "Proxy-Authorization: [REDACTED]\n")
	assert.Contains(t, stdout.String(), "Cookie: [REDACTED]\n")
	assert.NotContains(t, stdout.String(), "response-secret")
	assert.NotContains(t, stdout.String(), "response-auth-secret")
	assert.NotContains(t, stdout.String(), "response-proxy-secret")
	assert.NotContains(t, stdout.String(), "response-cookie-secret")
}

func TestRunAPI_VerboseHostLineUsesParsedHostOnly(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		return apiTestResponse(http.StatusNoContent, "", ""), nil
	})
	ac.BaseURL = "https://workspace.example/api/v1"
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Verbose: true}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "Host: workspace.example\n")
}

func TestRunAPI_VerboseShowsDefaultRequestContentType(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		return apiTestResponse(http.StatusNoContent, "", ""), nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Input: "-", Verbose: true}, &stdout, &stderr, strings.NewReader("input"))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Contains(t, stderr.String(), "Content-Type: application/json\n")
}

func TestRunAPI_VerboseShowsFinalUserAgentOnce(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "custom-agent", r.Header.Get("User-Agent"))
		return apiTestResponse(http.StatusNoContent, "", ""), nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Verbose: true, Headers: []string{"User-Agent: custom-agent"}}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, strings.Count(stderr.String(), "User-Agent:"))
	assert.Contains(t, stderr.String(), "User-Agent: custom-agent\n")
	assert.NotContains(t, stderr.String(), "User-Agent: alpacon-test\n")
}

func TestRunAPI_SanitizesResponseBodyOnlyWhenStdoutIsTerminal(t *testing.T) {
	previous := isStdoutTerminal
	t.Cleanup(func() { isStdoutTerminal = previous })
	cases := []struct {
		name string
		ct   string
		body string
	}{
		{"json esc", "application/json", "{\"note\":\"\x1b[31mred\x1b[0m\"}"},
		{"text c1", "text/plain", "before\u009bafter"},
		{"text del", "text/plain", "before\x7fafter"},
		{"text bidi", "text/plain", "before\u202eafter"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, tc.ct, tc.body), nil
			})

			isStdoutTerminal = func() bool { return true }
			var ttyOut, pipeErr bytes.Buffer
			_, err := runAPITest(ac, options{Endpoint: "/x"}, &ttyOut, &pipeErr, strings.NewReader(""))
			require.NoError(t, err)

			isStdoutTerminal = func() bool { return false }
			var pipeOut, ttyErr bytes.Buffer
			_, err = runAPITest(ac, options{Endpoint: "/x"}, &pipeOut, &ttyErr, strings.NewReader(""))
			require.NoError(t, err)

			assert.NotContains(t, ttyOut.String(), "\x1b")
			assert.NotContains(t, ttyOut.String(), "\u009b")
			assert.NotContains(t, ttyOut.String(), "\x7f")
			assert.NotContains(t, ttyOut.String(), "\u202e")
			assert.Equal(t, tc.body, pipeOut.String())
		})
	}
}

func TestRunAPI_TerminalBodyKeepsTabsWhileStrippingControls(t *testing.T) {
	previous := isStdoutTerminal
	t.Cleanup(func() { isStdoutTerminal = previous })
	isStdoutTerminal = func() bool { return true }

	cases := []struct {
		name string
		body string
		want string
	}{
		{"tsv body keeps tabs", "id\tname\n1\tweb\n", "id\tname\n1\tweb\n"},
		{"ansi escape inside tab segment", "a\x1b[31mb\tc", "ab\tc"},
		{"leading tab", "\tafter", "\tafter"},
		{"trailing tab", "before\t", "before\t"},
		{"consecutive tabs", "a\t\tb", "a\t\tb"},
		{"tab directly after escape", "\x1b\tx", "\tx"},
		{"other c0 controls stripped", "a\rb\x07c\x00d", "abcd"},
		{"newlines preserved", "line1\nline2\n", "line1\nline2\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "text/plain", tc.body), nil
			})
			var out bytes.Buffer
			_, err := runAPITest(ac, options{Endpoint: "/x"}, &out, &bytes.Buffer{}, strings.NewReader(""))
			require.NoError(t, err)
			assert.Equal(t, tc.want, out.String())
		})
	}
}

func TestRunAPI_FormatsJSONOnlyOnTerminalAndLeavesPipedOutputByteForByte(t *testing.T) {
	previous := isStdoutTerminal
	t.Cleanup(func() { isStdoutTerminal = previous })
	body := `{"a":1,"b":2}`
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		return apiTestResponse(http.StatusOK, "application/json", body), nil
	})

	isStdoutTerminal = func() bool { return true }
	var ttyOut bytes.Buffer
	_, err := runAPITest(ac, options{Endpoint: "/x"}, &ttyOut, &bytes.Buffer{}, strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, "{\n  \"a\": 1,\n  \"b\": 2\n}\n", ttyOut.String())

	isStdoutTerminal = func() bool { return false }
	var pipeOut bytes.Buffer
	_, err = runAPITest(ac, options{Endpoint: "/x"}, &pipeOut, &bytes.Buffer{}, strings.NewReader(""))
	require.NoError(t, err)
	assert.Equal(t, body, pipeOut.String())
}

func TestAPICommand_SilentPrintsNoBodyButStillReportsStatusOnError(t *testing.T) {
	cases := []struct {
		name       string
		status     int
		wantCode   int
		wantStderr string
	}{
		{"not found exits one with status line, no body", http.StatusNotFound, 1, fmt.Sprintf("HTTP %d\n", http.StatusNotFound)},
		{"ok prints nothing", http.StatusOK, 0, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previousClient, previousExit := newAPIClient, exitAPI
			t.Cleanup(func() { newAPIClient, exitAPI = previousClient, previousExit })
			newAPIClient = func() (*client.AlpaconClient, error) {
				return apiTestClient(func(r *http.Request) (*http.Response, error) {
					return apiTestResponse(tc.status, "application/json", `{"detail":"body"}`), nil
				}), nil
			}
			exitCode := 0
			exitAPI = func(code int) { exitCode = code }
			command := newCommand()
			command.SetArgs([]string{"--silent", "/x"})
			var executeErr error

			stdout, stderr := testutil.CaptureOutput(t, func() { executeErr = command.Execute() })

			require.NoError(t, executeErr)
			assert.Equal(t, tc.wantCode, exitCode)
			assert.Empty(t, stdout)
			assert.Equal(t, tc.wantStderr, stderr)
		})
	}
}

func TestAddFieldsToQuery_EmptyArrayKeepsQueryMarker(t *testing.T) {
	t.Parallel()
	path, err := addFieldsToQuery("/x", map[string]any{"empty": []any{}})

	require.NoError(t, err)
	assert.Equal(t, "/x?", path)
}

func TestAddFieldsToQuery_SpaceValueIsEncodedNotRaw(t *testing.T) {
	t.Parallel()
	path, err := addFieldsToQuery("/x", map[string]any{"search": "admin user"})

	require.NoError(t, err)
	assert.Equal(t, "/x?search=admin+user", path)
	assert.NotContains(t, path, " ")

	normalized, err := client.NormalizeRawEndpoint(path)
	require.NoError(t, err)
	assert.Equal(t, path, normalized)
}

func TestRunAPI_RejectsMalformedHeader(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"broken", "Bad Name: value", "Bad@Name: value", "X-Test: value\r\nX-Evil: yes", "X-Test: bad\x00value", "X-Test: bad\x1bvalue", "X-Test: bad\x7fvalue", "X-Test: bad\x01value"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Headers: []string{header}}, &stdout, &stderr, strings.NewReader(""))

			require.Error(t, err)
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
		})
	}
}

func TestRunAPI_RejectsUnsupportedSpecialHeaders(t *testing.T) {
	t.Parallel()
	for _, header := range []string{"Host: other.example", "Transfer-Encoding: chunked", "Trailer: X-Checksum"} {
		t.Run(header, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(http.StatusNoContent, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Headers: []string{header}, Verbose: true}, &stdout, &stderr, strings.NewReader(""))

			require.Error(t, err)
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
			assert.Empty(t, stderr.String())
		})
	}
}

func TestRunAPI_RejectsContentLengthOverrideBeforeRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		noInput bool
		headers []string
	}{
		{name: "matching value", headers: []string{"Content-Length: 5"}},
		{name: "zero with no body", noInput: true, headers: []string{"Content-Length: 0"}},
		{name: "post zero", headers: []string{"Content-Length: 0"}},
		{name: "mismatched value", headers: []string{"Content-Length: 4"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(http.StatusNoContent, "", ""), nil
			})
			var stdout, stderr bytes.Buffer
			opts := options{Endpoint: "/x", Headers: tc.headers, Verbose: true}
			if !tc.noInput {
				opts.Input = "-"
			}

			code, err := runAPITest(ac, opts, &stdout, &stderr, strings.NewReader("hello"))

			require.ErrorContains(t, err, "Content-Length")
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
			assert.Empty(t, stderr.String())
		})
	}
}

func TestRunAPI_RejectsExternalEndpoints(t *testing.T) {
	t.Parallel()
	for _, endpoint := range []string{"https://evil.example/x", "//evil.example/x", "http:/x"} {
		t.Run(endpoint, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: endpoint}, &stdout, &stderr, strings.NewReader(""))

			require.Error(t, err)
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
		})
	}
}

func TestRunAPI_ResponseOutput(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name       string
		opts       options
		status     int
		ct         string
		body       string
		wantStdout string
		wantStderr string
		wantCode   int
	}{
		{"json error", options{Endpoint: "/x"}, http.StatusNotFound, "application/json", `{"detail":"missing"}`, `{"detail":"missing"}`, "", 1},
		{"include", options{Endpoint: "/x", Include: true}, http.StatusOK, "text/csv", "a,b\n", "HTTP/1.1 200 OK\nContent-Type: text/csv\nX-Trace: trace-1\n\na,b\n", "", 0},
		{"silent", options{Endpoint: "/x", Silent: true}, http.StatusNotFound, "text/plain", "hidden", "", "", 1},
		{"verbose", options{Endpoint: "/x", Verbose: true}, http.StatusOK, "text/plain", "raw", "raw", "GET /x HTTP/1.1", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) { return apiTestResponse(tc.status, tc.ct, tc.body), nil })
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, tc.opts, &stdout, &stderr, strings.NewReader(""))

			if tc.wantCode != 0 {
				require.EqualError(t, err, "HTTP "+strconv.Itoa(tc.status))
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tc.wantCode, code)
			assert.Equal(t, tc.wantStdout, stdout.String())
			if tc.wantStderr != "" {
				assert.Contains(t, stderr.String(), tc.wantStderr)
			}
			assert.NotContains(t, stderr.String(), "secret-token")
		})
	}
}

func TestAPICommand_HTTPErrorPrintsExactLineAndExitsOne(t *testing.T) {
	previousClient, previousExit := newAPIClient, exitAPI
	t.Cleanup(func() { newAPIClient, exitAPI = previousClient, previousExit })
	newAPIClient = func() (*client.AlpaconClient, error) {
		return apiTestClient(func(r *http.Request) (*http.Response, error) {
			return apiTestResponse(http.StatusNotFound, "application/json", `{"detail":"missing"}`), nil
		}), nil
	}
	exitCode := 0
	exitAPI = func(code int) { exitCode = code }
	command := newCommand()
	command.SetArgs([]string{"/x"})
	var executeErr error

	stdout, stderr := testutil.CaptureOutput(t, func() { executeErr = command.Execute() })

	require.NoError(t, executeErr)
	assert.Equal(t, 1, exitCode)
	assert.JSONEq(t, `{"detail":"missing"}`, stdout)
	assert.Equal(t, fmt.Sprintf("HTTP %d\n", http.StatusNotFound), stderr)
}

func TestAPICommand_RedirectLinePrintsOnlyForRedirectWithLocation(t *testing.T) {
	cases := []struct {
		name     string
		status   int
		location string
		want     string
	}{
		{name: "found with location", status: http.StatusFound, location: "https://evil.example/x", want: "HTTP 302\nredirect to https://evil.example/x not followed\n"},
		{name: "not modified without location", status: http.StatusNotModified, want: "HTTP 304\n"},
		{name: "bad request with location", status: http.StatusBadRequest, location: "https://evil.example/x", want: "HTTP 400\n"},
		{name: "unauthorized with location", status: http.StatusUnauthorized, location: "https://evil.example/x", want: "HTTP 401\n"},
		{name: "not found with location", status: http.StatusNotFound, location: "https://evil.example/x", want: "HTTP 404\n"},
		{name: "server error with location", status: http.StatusInternalServerError, location: "https://evil.example/x", want: "HTTP 500\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			previousClient, previousExit := newAPIClient, exitAPI
			t.Cleanup(func() { newAPIClient, exitAPI = previousClient, previousExit })
			newAPIClient = func() (*client.AlpaconClient, error) {
				return apiTestClient(func(r *http.Request) (*http.Response, error) {
					response := apiTestResponse(tc.status, "text/plain", "")
					if tc.location != "" {
						response.Header.Set("Location", tc.location)
					}
					return response, nil
				}), nil
			}
			exitCode := 0
			exitAPI = func(code int) { exitCode = code }
			command := newCommand()
			command.SetArgs([]string{"/x"})
			var executeErr error

			_, stderr := testutil.CaptureOutput(t, func() { executeErr = command.Execute() })

			require.NoError(t, executeErr)
			assert.Equal(t, 1, exitCode)
			assert.Equal(t, tc.want, stderr)
		})
	}
}

func TestAPICommand_MalformedHeaderExitsTwo(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				_, _ = io.WriteString(os.Stderr, "REQUEST_SENT\n")
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-H", "X-Test: bad\x1bvalue", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MalformedHeaderExitsTwo$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.Contains(t, string(output), "invalid header")
	assert.NotContains(t, string(output), "REQUEST_SENT")
}

func TestAPICommand_InvalidMethodExitsTwoWithoutBuildingClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				_, _ = io.WriteString(os.Stderr, "REQUEST_SENT\n")
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-X", "GET /x", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_InvalidMethodExitsTwoWithoutBuildingClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "REQUEST_SENT")
}

func TestAPICommand_MalformedHeaderDoesNotLeakValue(t *testing.T) {
	cases := []struct {
		name   string
		header string
	}{
		{"authorization cr", "Authorization: Bearer secret-marker\r"},
		{"authorization lf", "authorization: Bearer secret-marker\n"},
		{"authorization esc", "AUTHORIZATION: Bearer secret-marker\x1b"},
		{"authorization nul", "Authorization: Bearer secret-marker\x00"},
		{"authorization del", "aUtHoRiZaTiOn: Bearer secret-marker\x7f"},
		{"authorization no colon", "Authorization Bearer secret-marker"},
		{"other invalid name", "X Bad: secret-marker"},
	}
	if index, err := strconv.Atoi(os.Getenv("ALPACON_API_HEADER_CASE")); err == nil {
		newAPIClient = func() (*client.AlpaconClient, error) {
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				_, _ = io.WriteString(os.Stderr, "REQUEST_SENT\n")
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-H", cases[index].header, "/x"})
		if execErr := command.Execute(); execErr != nil {
			_, _ = io.WriteString(os.Stderr, execErr.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	for index, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MalformedHeaderDoesNotLeakValue$")
			child.Env = append(os.Environ(), "ALPACON_API_HEADER_CASE="+strconv.Itoa(index))
			output, err := child.CombinedOutput()
			var exitErr *exec.ExitError

			require.ErrorAs(t, err, &exitErr)
			assert.Equal(t, 2, exitErr.ExitCode())
			assert.Contains(t, string(output), "invalid header")
			assert.NotContains(t, string(output), "secret-marker")
			assert.NotContains(t, string(output), "REQUEST_SENT")
		})
	}
}

func TestAPICommand_MalformedEndpointExitsTwoWithoutClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"http://evil.example/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MalformedEndpointExitsTwoWithoutClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_MalformedFieldExitsTwoWithoutClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-f", "noequals", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MalformedFieldExitsTwoWithoutClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_MissingFieldFileExitsTwoWithoutClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-F", "a=@/nonexistent-file-for-test", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MissingFieldFileExitsTwoWithoutClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_MissingInputFileExitsTwoWithoutClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"--input", "/nonexistent-input-for-test", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_MissingInputFileExitsTwoWithoutClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_BadContentLengthExitsTwoWithoutClient(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-H", "Content-Length: 4", "--input", "-", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_BadContentLengthExitsTwoWithoutClient$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	child.Stdin = strings.NewReader("hello")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_RejectsTwoStdinFields(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-F", "a=@-", "-F", "b=@-", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_RejectsTwoStdinFields$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.Contains(t, string(output), "only one field can be read from standard input")
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

func TestAPICommand_RejectsStdinFieldWithStdinInput(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			_, _ = io.WriteString(os.Stderr, "CLIENT_CONSTRUCTED\n")
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				return apiTestResponse(http.StatusOK, "application/json", `{}`), nil
			}), nil
		}
		command := newCommand()
		command.SetArgs([]string{"-F", "a=@-", "--input", "-", "/x"})
		if err := command.Execute(); err != nil {
			_, _ = io.WriteString(os.Stderr, err.Error())
			os.Exit(5)
		}
		os.Exit(0)
	}
	child := exec.Command(os.Args[0], "-test.run=^TestAPICommand_RejectsStdinFieldWithStdinInput$")
	child.Env = append(os.Environ(), "ALPACON_API_USAGE_CHILD=1")
	output, err := child.CombinedOutput()
	var exitErr *exec.ExitError

	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, 2, exitErr.ExitCode())
	assert.Contains(t, string(output), "only one field can be read from standard input")
	assert.NotContains(t, string(output), "CLIENT_CONSTRUCTED")
}

// mfaTestServer points HOME at a temp dir holding a refresh token, so its caller must stay serial.
func mfaTestServer(t *testing.T, endpointResponses func(callCount int) (int, string, string)) (*client.AlpaconClient, *int32) {
	t.Helper()
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ALPACON_NO_BROWSER", "1")

	require.NoError(t, config.CreateConfig(
		"https://workspace.example", "my-workspace",
		"", "", "old-token", "refresh-token", "", 0, false,
	))

	var endpointCalls int32
	ac := &client.AlpaconClient{
		BaseURL:       "https://workspace.example",
		WorkspaceName: "my-workspace",
		HTTPClient: &http.Client{Transport: apiRoundTripFunc(func(r *http.Request) (*http.Response, error) {
			switch r.URL.Path {
			case "/x":
				n := int(atomic.AddInt32(&endpointCalls, 1))
				status, ct, body := endpointResponses(n)
				return apiTestResponse(status, ct, body), nil
			case "/api/auth0/mfa/":
				return apiTestResponse(http.StatusOK, "application/json", `{"mfa_url": "https://example.com/mfa"}`), nil
			case "/api/auth0/mfa/completion/":
				return apiTestResponse(http.StatusOK, "application/json", `{"completed": true}`), nil
			case "/api/auth/env/":
				return apiTestResponse(http.StatusOK, "application/json", `{"auth0": {"domain": "auth0.example", "client_id": "cid", "audience": "aud", "schema_name": "acme"}}`), nil
			case "/oauth/token/":
				return apiTestResponse(http.StatusOK, "application/json", `{"access_token": "new-token", "expires_in": 3600, "token_type": "Bearer"}`), nil
			default:
				t.Fatalf("unexpected request to %s", r.URL.Path)
				return nil, nil
			}
		})},
	}
	ac.SetAccessToken("old-token")
	return ac, &endpointCalls
}

func TestRunAPI_MFARequiredOpensLinkAndRetriesOnce(t *testing.T) {
	ac, calls := mfaTestServer(t, func(n int) (int, string, string) {
		if n == 1 {
			return http.StatusUnauthorized, "application/json", `{"code":"auth_mfa_required"}`
		}
		return http.StatusOK, "application/json", `{"ok":true}`
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x"}, &stdout, &stderr, strings.NewReader(""))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.JSONEq(t, `{"ok":true}`, stdout.String())
	assert.Equal(t, int32(2), atomic.LoadInt32(calls), "the endpoint must be hit exactly twice")
}

func TestRunAPI_CallerAuthorizationHeaderSkipsMFA(t *testing.T) {
	ac, calls := mfaTestServer(t, func(int) (int, string, string) {
		return http.StatusUnauthorized, "application/json", `{"code":"auth_mfa_required"}`
	})
	var stdout, stderr bytes.Buffer
	opts := options{Endpoint: "/x", Headers: []string{"Authorization: Bearer caller-token"}}
	request, code, err := prepareRequest(opts, strings.NewReader(""))
	require.NoError(t, err)
	require.Zero(t, code)

	code, err = runAPI(ac, opts, request, &stdout, &stderr)

	require.EqualError(t, err, fmt.Sprintf("HTTP %d", http.StatusUnauthorized))
	assert.Equal(t, 1, code)
	assert.JSONEq(t, `{"code":"auth_mfa_required"}`, stdout.String())
	assert.Equal(t, int32(1), atomic.LoadInt32(calls), "the endpoint must be hit exactly once")
}

func TestRunAPI_DifferentCodeSkipsMFA(t *testing.T) {
	ac, calls := mfaTestServer(t, func(int) (int, string, string) {
		return http.StatusUnauthorized, "application/json", `{"code":"auth_authentication_failed"}`
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x"}, &stdout, &stderr, strings.NewReader(""))

	require.EqualError(t, err, fmt.Sprintf("HTTP %d", http.StatusUnauthorized))
	assert.Equal(t, 1, code)
	assert.JSONEq(t, `{"code":"auth_authentication_failed"}`, stdout.String())
	assert.Equal(t, int32(1), atomic.LoadInt32(calls), "the endpoint must be hit exactly once")
}
