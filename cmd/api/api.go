package apicmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

var (
	ApiCmd           = newCommand()
	newAPIClient     = client.NewAlpaconAPIClient
	exitAPI          = os.Exit
	isStdoutTerminal = func() bool { return term.IsTerminal(int(os.Stdout.Fd())) }
)

type options struct {
	Endpoint  string
	Method    string
	MethodSet bool
	Fields    []fieldInput
	Headers   []string
	Input     string
	Include   bool
	Silent    bool
	Verbose   bool
}

type httpStatusError struct{ status int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("HTTP %d", e.status) }

type fieldFlag struct {
	fields *[]fieldInput
	typed  bool
}

func (f *fieldFlag) String() string { return "" }
func (f *fieldFlag) Type() string   { return "field" }
func (f *fieldFlag) Set(value string) error {
	*f.fields = append(*f.fields, fieldInput{Raw: value, Typed: f.typed})
	return nil
}

// preparedRequest is everything runAPI needs to send: request preparation and
// local validation are done, so the client is built only for a request that
// is already known to be well formed.
type preparedRequest struct {
	Method  string
	Path    string
	Body    []byte
	Headers http.Header
}

func newCommand() *cobra.Command {
	var opts options
	cmd := &cobra.Command{
		Use:   "api [flags] ENDPOINT",
		Short: "Send a request to the Alpacon API",
		Long: "Send a request to the Alpacon API. ENDPOINT is a path on the current workspace—an absolute URL is refused.\n" +
			"Fields (-f/-F) go to the query string for GET, and to a JSON body otherwise. A non-2xx response's body is still printed\n" +
			"to stdout, while `HTTP <code>` goes to stderr and the command exits 1.",
		Example: "  alpacon api /api/iam/users/\n  alpacon api -X POST -F name=my-server /api/servers/servers/\n  alpacon api -i /api/iam/users/-/",
		Run: func(cmd *cobra.Command, args []string) {
			if len(args) != 1 {
				utils.CliErrorWithExitCode(utils.ExitCodeUsageError, "expected one ENDPOINT")
			}
			opts.Endpoint = args[0]
			opts.MethodSet = cmd.Flags().Changed("method")
			request, code, err := prepareRequest(opts, os.Stdin)
			if err != nil {
				utils.CliErrorWithExitCode(code, "%s", err)
			}
			ac, err := newAPIClient()
			if err != nil {
				utils.CliErrorWithExit("Connection to Alpacon API failed: %s", err)
			}
			code, err = runAPI(ac, opts, request, cmd.OutOrStdout(), cmd.ErrOrStderr())
			if err != nil {
				if statusErr, ok := err.(*httpStatusError); ok {
					if _, writeErr := fmt.Fprintf(cmd.ErrOrStderr(), "HTTP %d\n", statusErr.status); writeErr != nil {
						utils.CliErrorWithExitCode(utils.ExitCodeGeneralError, "%s", writeErr)
					}
					exitAPI(utils.ExitCodeGeneralError)
					return
				}
				utils.CliErrorWithExitCode(code, "%s", err)
			}
			if code != 0 {
				exitAPI(code)
			}
		},
	}
	cmd.Flags().StringVarP(&opts.Method, "method", "X", "GET", "HTTP method")
	cmd.Flags().VarP(&fieldFlag{fields: &opts.Fields}, "raw-field", "f", "String field (key=value)")
	cmd.Flags().VarP(&fieldFlag{fields: &opts.Fields, typed: true}, "field", "F", "Typed field (key=value)")
	cmd.Flags().StringArrayVarP(&opts.Headers, "header", "H", nil, "Request header (key:value; Host, Transfer-Encoding, Trailer, Content-Length unsupported)")
	cmd.Flags().StringVar(&opts.Input, "input", "", "Request body file (- for stdin)")
	cmd.Flags().BoolVarP(&opts.Include, "include", "i", false, "Include response headers")
	cmd.Flags().BoolVar(&opts.Silent, "silent", false, "Suppress response body")
	cmd.Flags().BoolVar(&opts.Verbose, "verbose", false, "Print request and response headers to stderr")
	return cmd
}

// checkSingleStdinConsumer refuses more than one reader of stdin: two `-F
// k=@-` fields, or a `-F k=@-` field together with `--input -`. Stdin can
// only be read once, so a second consumer would silently see nothing.
func checkSingleStdinConsumer(opts options) error {
	consumers := 0
	for _, field := range opts.Fields {
		if !field.Typed {
			continue
		}
		_, value, hasValue := strings.Cut(field.Raw, "=")
		if hasValue && value == "@-" {
			consumers++
		}
	}
	if opts.Input == "-" {
		consumers++
	}
	if consumers > 1 {
		return fmt.Errorf("only one field can be read from standard input")
	}
	return nil
}

// prepareRequest runs every check and read that needs no client: endpoint,
// header, and field validation, the @file/@- and --input reads, and the
// Content-Length check. It fails before newAPIClient can make a network call
// or refresh a token, so a usage error never constructs a client.
func prepareRequest(opts options, stdin io.Reader) (preparedRequest, int, error) {
	path, err := client.NormalizeRawEndpoint(opts.Endpoint)
	if err != nil {
		return preparedRequest{}, utils.ExitCodeUsageError, err
	}
	if err := checkSingleStdinConsumer(opts); err != nil {
		return preparedRequest{}, utils.ExitCodeUsageError, err
	}
	fields, err := parseFields(opts.Fields, stdin)
	if err != nil {
		return preparedRequest{}, utils.ExitCodeUsageError, err
	}
	method := strings.ToUpper(opts.Method)
	if method == "" {
		method = http.MethodGet
	}
	if !opts.MethodSet && (len(opts.Fields) > 0 || opts.Input != "") {
		method = http.MethodPost
	}
	if !isToken(method) {
		return preparedRequest{}, utils.ExitCodeUsageError, fmt.Errorf("invalid method")
	}
	var body []byte // read whole so a stale-401 renewal can replay it
	if opts.Input != "" {
		if opts.Input == "-" {
			body, err = io.ReadAll(stdin)
		} else {
			body, err = os.ReadFile(opts.Input)
		}
		if err != nil {
			return preparedRequest{}, utils.ExitCodeUsageError, err
		}
	}
	queryFields := method == http.MethodGet || method == http.MethodHead || opts.Input != ""
	if queryFields && len(fields) > 0 {
		path, err = addFieldsToQuery(path, fields)
		if err != nil {
			return preparedRequest{}, utils.ExitCodeUsageError, err
		}
	} else if len(fields) > 0 {
		body, err = json.Marshal(fields)
		if err != nil {
			return preparedRequest{}, utils.ExitCodeUsageError, err
		}
	}
	requestHeaders, err := parseHeaders(opts.Headers)
	if err != nil {
		return preparedRequest{}, utils.ExitCodeUsageError, err
	}
	if err := client.ValidateRawRequestHeaders(method, requestHeaders); err != nil {
		return preparedRequest{}, utils.ExitCodeUsageError, err
	}
	_, contentTypeSet := requestHeaders["Content-Type"]
	if len(fields) > 0 && !queryFields && !contentTypeSet {
		requestHeaders.Set("Content-Type", "application/json")
	}
	return preparedRequest{Method: method, Path: path, Body: body, Headers: requestHeaders}, 0, nil
}

func runAPI(ac *client.AlpaconClient, opts options, request preparedRequest, stdout, stderr io.Writer) (int, error) {
	if opts.Verbose {
		if err := printRequest(stderr, ac, request.Method, request.Path, request.Headers); err != nil {
			return utils.ExitCodeGeneralError, err
		}
	}
	var reader io.Reader
	if request.Body != nil {
		reader = bytes.NewReader(request.Body)
	}
	response, err := ac.SendRawRequest(request.Method, request.Path, reader, request.Headers)
	if err != nil {
		return utils.ExitCodeGeneralError, err
	}
	if opts.Verbose {
		if err := printResponseHeaders(stderr, response); err != nil {
			return utils.ExitCodeGeneralError, err
		}
	}
	if opts.Include {
		if err := printResponseHeaders(stdout, response); err != nil {
			return utils.ExitCodeGeneralError, err
		}
	}
	if !opts.Silent && len(response.Body) > 0 {
		output := response.Body
		if isStdoutTerminal() {
			contentType, _, _ := mime.ParseMediaType(response.Header.Get("Content-Type"))
			if contentType == "application/json" || strings.HasSuffix(contentType, "+json") {
				var formatted bytes.Buffer
				if json.Indent(&formatted, output, "", "  ") == nil {
					formatted.WriteByte('\n')
					output = formatted.Bytes()
				}
			}
			output = []byte(sanitizeTerminalBodyKeepingTabs(string(output)))
		}
		if _, err := stdout.Write(output); err != nil {
			return utils.ExitCodeGeneralError, err
		}
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return utils.ExitCodeGeneralError, &httpStatusError{status: response.StatusCode}
	}
	return 0, nil
}

// SanitizeTerminalBlock strips tabs, so each tab-separated segment is sanitized on its own.
func sanitizeTerminalBodyKeepingTabs(body string) string {
	segments := strings.Split(body, "\t")
	for i, segment := range segments {
		segments[i], _ = utils.SanitizeTerminalBlock(segment)
	}
	return strings.Join(segments, "\t")
}

func parseHeaders(raw []string) (http.Header, error) {
	result := make(http.Header)
	for _, entry := range raw {
		name, value, ok := strings.Cut(entry, ":")
		if !ok || !isToken(name) || !validHeaderValue(value) {
			return nil, fmt.Errorf("invalid header")
		}
		result.Add(name, strings.TrimSpace(value))
	}
	return result, nil
}

// isToken checks the RFC 7230 token grammar shared by a header name and an
// HTTP method: net/http applies the same rule to both.
func isToken(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		char := name[i]
		if (char >= 'A' && char <= 'Z') || (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(char)) {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	for i := range len(value) {
		char := value[i]
		if (char < 0x20 && char != '\t') || char == 0x7f {
			return false
		}
	}
	return true
}

func addFieldsToQuery(path string, fields map[string]any) (string, error) {
	parsed, err := url.Parse(path)
	if err != nil {
		return "", err
	}
	query := make(url.Values)
	for key, value := range fields {
		appendQueryField(query, key, value)
	}
	if parsed.RawQuery != "" {
		parsed.RawQuery += "&"
	}
	parsed.RawQuery += query.Encode()
	if parsed.RawQuery == "" {
		parsed.ForceQuery = true
	}
	return parsed.String(), nil
}

func appendQueryField(query url.Values, key string, value any) {
	switch typed := value.(type) {
	case map[string]any:
		keys := make([]string, 0, len(typed))
		for name := range typed {
			keys = append(keys, name)
		}
		sort.Strings(keys)
		for _, name := range keys {
			appendQueryField(query, key+"["+name+"]", typed[name])
		}
	case []any:
		for _, item := range typed {
			appendQueryField(query, key+"[]", item)
		}
	case nil:
		query.Add(key, "")
	default:
		query.Add(key, fmt.Sprint(typed))
	}
}

func printRequest(w io.Writer, ac *client.AlpaconClient, method, path string, header http.Header) error {
	header = header.Clone()
	if _, hasContentType := header["Content-Type"]; !hasContentType && (method == http.MethodPost || method == http.MethodPatch || method == http.MethodPut) {
		header.Set("Content-Type", "application/json")
	}
	if _, err := fmt.Fprintf(w, "%s %s HTTP/1.1\n", safeTerminal(method), safeTerminal(path)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "Host: %s\n", safeTerminal(strings.TrimPrefix(strings.TrimPrefix(ac.BaseURL, "https://"), "http://"))); err != nil {
		return err
	}
	if ac.AccessToken() != "" || ac.Token != "" || header.Get("Authorization") != "" {
		if _, err := io.WriteString(w, "Authorization: [REDACTED]\n"); err != nil {
			return err
		}
	}
	_, userAgentOverridden := header["User-Agent"]
	if ac.UserAgent != "" && !userAgentOverridden {
		if _, err := fmt.Fprintf(w, "User-Agent: %s\n", safeTerminal(ac.UserAgent)); err != nil {
			return err
		}
	}
	for _, name := range sortedHeaderNames(header) {
		if strings.EqualFold(name, "Authorization") {
			continue
		}
		values := header.Values(name)
		if isSensitiveHeader(name) {
			values = []string{"[REDACTED]"}
		}
		for _, value := range values {
			if _, err := fmt.Fprintf(w, "%s: %s\n", safeTerminal(name), safeTerminal(value)); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

func printResponseHeaders(w io.Writer, response *client.RawResponse) error {
	status := response.Status
	if !strings.HasPrefix(status, strconv.Itoa(response.StatusCode)) {
		status = fmt.Sprintf("%d %s", response.StatusCode, status)
	}
	if _, err := fmt.Fprintf(w, "%s %s\n", safeTerminal(response.Proto), safeTerminal(status)); err != nil {
		return err
	}
	for _, name := range sortedHeaderNames(response.Header) {
		values := response.Header.Values(name)
		if isSensitiveHeader(name) {
			values = []string{"[REDACTED]"}
		}
		for _, value := range values {
			if _, err := fmt.Fprintf(w, "%s: %s\n", safeTerminal(name), safeTerminal(value)); err != nil {
				return err
			}
		}
	}
	_, err := io.WriteString(w, "\n")
	return err
}

// isSensitiveHeader is the redaction list for both request and response
// headers, under both --verbose and -i/--include.
func isSensitiveHeader(name string) bool {
	return strings.EqualFold(name, "Authorization") || strings.EqualFold(name, "Proxy-Authorization") || strings.EqualFold(name, "Cookie") || strings.EqualFold(name, "Set-Cookie")
}

func safeTerminal(value string) string {
	clean, _ := utils.SanitizeTerminalBlock(value)
	return clean
}

func sortedHeaderNames(header http.Header) []string {
	names := make([]string, 0, len(header))
	for name := range header {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
