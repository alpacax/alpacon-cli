package apicmd

import (
	"bytes"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/client"
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
				return apiTestResponse(204, "", ""), nil
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
				return apiTestResponse(204, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Fields: []fieldInput{{"name=one", false}}, Headers: []string{"Content-Type: " + tc.want}}, &stdout, &stderr, strings.NewReader(""))

			require.NoError(t, err)
			assert.Equal(t, 0, code)
		})
	}
}

func TestRunAPI_HeaderOutputStripsTerminalControls(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		response := apiTestResponse(200, "text/plain", "body")
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
		response := apiTestResponse(200, "text/plain", "body")
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
		response := apiTestResponse(200, "text/plain", "body")
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

func TestRunAPI_VerboseShowsDefaultRequestContentType(t *testing.T) {
	t.Parallel()
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))
		return apiTestResponse(204, "", ""), nil
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
		return apiTestResponse(204, "", ""), nil
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
				return apiTestResponse(200, tc.ct, tc.body), nil
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

func TestRunAPI_FormatsJSONOnlyOnTerminalAndLeavesPipedOutputByteForByte(t *testing.T) {
	previous := isStdoutTerminal
	t.Cleanup(func() { isStdoutTerminal = previous })
	body := `{"a":1,"b":2}`
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		return apiTestResponse(200, "application/json", body), nil
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
		{"404 exits one with status line, no body", 404, 1, "HTTP 404\n"},
		{"200 prints nothing", 200, 0, ""},
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(204, "", ""), nil
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

func TestRunAPI_ContentLengthMatchesRequestAndVerbose(t *testing.T) {
	t.Parallel()
	requests := 0
	ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
		requests++
		assert.Equal(t, int64(5), r.ContentLength)
		assert.Empty(t, r.Header.Values("Content-Length"))
		body, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		assert.Equal(t, "hello", string(body))
		return apiTestResponse(204, "", ""), nil
	})
	var stdout, stderr bytes.Buffer

	code, err := runAPITest(ac, options{Endpoint: "/x", Input: "-", Headers: []string{"Content-Length: 5"}, Verbose: true}, &stdout, &stderr, strings.NewReader("hello"))

	require.NoError(t, err)
	assert.Equal(t, 0, code)
	assert.Equal(t, 1, requests)
	assert.Equal(t, 1, strings.Count(stderr.String(), "Content-Length: 5\n"))
}

func TestRunAPI_RejectsInvalidContentLengthBeforeRequest(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		method  string
		noInput bool
		headers []string
	}{
		{name: "negative", headers: []string{"Content-Length: -1"}},
		{name: "non-numeric", headers: []string{"Content-Length: abc"}},
		{name: "mismatched with body", headers: []string{"Content-Length: 4"}},
		{name: "duplicate", headers: []string{"Content-Length: 5", "Content-Length: 5"}},
		{name: "leading zero", headers: []string{"Content-Length: 05"}},
		{name: "get zero with no body", method: http.MethodGet, noInput: true, headers: []string{"Content-Length: 0"}},
		{name: "head zero with no body", method: http.MethodHead, noInput: true, headers: []string{"Content-Length: 0"}},
		{name: "post nonzero with no body", method: http.MethodPost, noInput: true, headers: []string{"Content-Length: 5"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(204, "", ""), nil
			})
			var stdout, stderr bytes.Buffer
			opts := options{Endpoint: "/x", Headers: tc.headers, Verbose: true}
			if !tc.noInput {
				opts.Input = "-"
			}
			if tc.method != "" {
				opts.Method, opts.MethodSet = tc.method, true
			}

			code, err := runAPITest(ac, opts, &stdout, &stderr, strings.NewReader("hello"))

			require.Error(t, err)
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
			assert.Empty(t, stderr.String())
		})
	}
}

func TestRunAPI_RejectsZeroContentLengthForEmptyGETOrHEAD(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodGet, http.MethodHead} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				return apiTestResponse(204, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Method: method, MethodSet: true, Input: "-", Headers: []string{"Content-Length: 0", "Authorization: Bearer secret-marker"}, Verbose: true}, &stdout, &stderr, strings.NewReader(""))

			require.Error(t, err)
			assert.Equal(t, 2, code)
			assert.Equal(t, 0, requests)
			assert.NotContains(t, err.Error(), "secret-marker")
			assert.Empty(t, stderr.String())
		})
	}
}

func TestRunAPI_ContentLengthZeroOnBodylessNonGetHead(t *testing.T) {
	t.Parallel()
	for _, method := range []string{http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			requests := 0
			ac := apiTestClient(func(r *http.Request) (*http.Response, error) {
				requests++
				assert.Empty(t, r.Header.Values("Content-Length"))
				assert.Nil(t, r.Body)
				return apiTestResponse(204, "", ""), nil
			})
			var stdout, stderr bytes.Buffer

			code, err := runAPITest(ac, options{Endpoint: "/x", Method: method, MethodSet: true, Headers: []string{"Content-Length: 0"}}, &stdout, &stderr, strings.NewReader(""))

			require.NoError(t, err)
			assert.Equal(t, 0, code)
			assert.Equal(t, 1, requests)
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
		{"json error", options{Endpoint: "/x"}, 404, "application/json", `{"detail":"missing"}`, `{"detail":"missing"}`, "", 1},
		{"include", options{Endpoint: "/x", Include: true}, 200, "text/csv", "a,b\n", "HTTP/1.1 200 OK\nContent-Type: text/csv\nX-Trace: trace-1\n\na,b\n", "", 0},
		{"silent", options{Endpoint: "/x", Silent: true}, 404, "text/plain", "hidden", "", "", 1},
		{"verbose", options{Endpoint: "/x", Verbose: true}, 200, "text/plain", "raw", "raw", "GET /x HTTP/1.1", 0},
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
			return apiTestResponse(404, "application/json", `{"detail":"missing"}`), nil
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
	assert.Equal(t, "HTTP 404\n", stderr)
}

func TestAPICommand_MalformedHeaderExitsTwo(t *testing.T) {
	if os.Getenv("ALPACON_API_USAGE_CHILD") == "1" {
		newAPIClient = func() (*client.AlpaconClient, error) {
			return apiTestClient(func(r *http.Request) (*http.Response, error) {
				_, _ = io.WriteString(os.Stderr, "REQUEST_SENT\n")
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
				return apiTestResponse(200, "application/json", `{}`), nil
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
