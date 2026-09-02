package utils

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/term"
)

const (
	// DefaultApprovalWaitTimeout is the shared --wait default so exec, work-session create, and event wait match; not a ceiling (--wait-approval exceeds it), preserving the old 30 × 10s window.
	DefaultApprovalWaitTimeout = 5 * time.Minute
)

var (
	uuidRegex = regexp.MustCompile(
		`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$` +
			`|^[0-9a-fA-F]{32}$`,
	)

	// ansiEscapeRE matches ANSI/VT escape sequences: CSI, OSC (BEL or ST), DCS/SOS/PM/APC string controls (ST), and Fe/Fs/nF sequences.
	ansiEscapeRE = regexp.MustCompile(`\x1b(?:\][^\x07\x1b]*(?:\x07|\x1b\\)|[PX^_][^\x1b]*\x1b\\|\[[\x30-\x3f]*[\x20-\x2f]*[@-~]|[\x20-\x2f]*[\x40-\x7e])`)
)

// ShowLogo renders the Pacabot mascot with up to 3 lines of text. When the
// terminal is wide enough the text sits to the right of the art (lines 1-3
// align with art rows 1-3); otherwise it falls back to rendering text below
// the art so wrapped lines don't break the half-block pixel art rendering.
//
// Pixel art ported from strategy/brand-assets/pacabot/pacabot.sh. In color
// mode, body cells render as spaces with BG=cyan so the cell background
// fills any inter-line gap. In plain mode (no TTY / NO_COLOR), body cells
// substitute █ glyphs so the silhouette stays visible without ANSI codes.
// Edge half-blocks (▄ ▀) are kept in both modes — they define the shape:
// ▄ corners and ! bar bottom (FG cyan, glyph extends below the em-box in
// most fonts), and ▀ for the eye (grey on cyan) and the mouth (grey FG).
// Brand colors: primary #27AAE1, secondary #58595B.
func ShowLogo(rightLines []string) {
	useColor := os.Getenv("NO_COLOR") == "" && term.IsTerminal(int(os.Stderr.Fd()))

	var p, pb, s, a, b, r string
	if useColor {
		p = "\033[38;2;39;170;225m"               // primary cyan FG (edges)
		pb = "\033[48;2;39;170;225m"              // primary cyan BG (solid body — applied to spaces)
		s = "\033[38;2;88;89;91m"                 // secondary grey FG (mouth + tagline)
		a = "\033[38;2;88;89;91;48;2;39;170;225m" // grey on cyan (eye)
		b = "\033[1m"                             // bold
		r = "\033[0m"
	}

	// Clamp to 3 lines so inline and stacked layouts behave consistently
	// regardless of how many lines the caller provides.
	if len(rightLines) > 3 {
		rightLines = rightLines[:3]
	}
	for len(rightLines) < 3 {
		rightLines = append(rightLines, "")
	}

	header := rightLines[0]
	if header != "" {
		header = b + p + header + r
	}
	tinted := func(line string) string {
		if line == "" {
			return ""
		}
		return s + line + r
	}

	// Art occupies columns 0..22 (visual). If the terminal is too narrow to
	// fit the longest right-side line beside the art, wrapped text would
	// render between art rows and break the pixel art. Detect and fall back
	// to printing text below the art.
	const artWidth = 22
	maxRight := 0
	for _, line := range rightLines {
		if n := utf8.RuneCountInString(line); n > maxRight {
			maxRight = n
		}
	}
	// Default to the stacked layout when terminal size is unknown — we'd
	// rather print a clean vertical layout than risk wrapping text into
	// the middle of the pixel art.
	cols, _, err := term.GetSize(int(os.Stderr.Fd()))
	inline := err == nil && cols >= artWidth+maxRight+1

	// Body cells render as spaces with BG=cyan in color mode; in plain mode
	// we substitute █ glyphs so the art stays visible without ANSI codes.
	bodyEar, bodyEleven, bodyTen, bodyEight, bodyTwo, bodyBar := "  ", "           ", "          ", "        ", "  ", " "
	if !useColor {
		bodyEar, bodyEleven, bodyTen, bodyEight, bodyTwo, bodyBar = "██", "███████████", "██████████", "████████", "██", "█"
	}

	fmt.Fprintln(os.Stderr)
	if inline {
		fmt.Fprintf(os.Stderr, "   %s%s%s  %s%s%s      %s%s%s      %s\n", pb, bodyEar, r, pb, bodyEar, r, pb, bodyBar, r, header)
		fmt.Fprintf(os.Stderr, " %s%s%s   %s▄%s      %s\n", pb, bodyEleven, r, p, r, tinted(rightLines[1]))
		fmt.Fprintf(os.Stderr, " %s%s%s%s▀%s%s%s%s%s▄%s         %s\n", pb, bodyEight, r, a, r, pb, bodyTwo, r, p, r, tinted(rightLines[2]))
		fmt.Fprintf(os.Stderr, "  %s%s%s%s▀%s\n", pb, bodyTen, r, s, r)
	} else {
		fmt.Fprintf(os.Stderr, "   %s%s%s  %s%s%s      %s%s%s\n", pb, bodyEar, r, pb, bodyEar, r, pb, bodyBar, r)
		fmt.Fprintf(os.Stderr, " %s%s%s   %s▄%s\n", pb, bodyEleven, r, p, r)
		fmt.Fprintf(os.Stderr, " %s%s%s%s▀%s%s%s%s%s▄%s\n", pb, bodyEight, r, a, r, pb, bodyTwo, r, p, r)
		fmt.Fprintf(os.Stderr, "  %s%s%s%s▀%s\n", pb, bodyTen, r, s, r)
		if header != "" {
			fmt.Fprintf(os.Stderr, "\n  %s\n", header)
		}
		for _, line := range rightLines[1:] {
			if line != "" {
				fmt.Fprintf(os.Stderr, "  %s\n", tinted(line))
			}
		}
	}
	fmt.Fprintln(os.Stderr)
}

func GetUserAgent() string {
	return fmt.Sprintf("%s/%s", "alpacon-cli", GetCLIVersion())
}

// ExtractBaseDomain extracts the top-level base domain from a workspace URL.
// For example, "https://myws.us1.alpacon.io" returns "alpacon.io".
// Returns "" if the hostname has fewer than 3 parts (e.g. self-hosted with no subdomain).
func ExtractBaseDomain(workspaceURL string) string {
	parsedURL, err := url.Parse(workspaceURL)
	if err != nil {
		return ""
	}

	hostname := parsedURL.Hostname()
	parts := strings.Split(hostname, ".")
	if len(parts) < 3 {
		return ""
	}

	return strings.Join(parts[len(parts)-2:], ".")
}

// ExtractWorkspaceName extracts workspace name from workspace URL
func ExtractWorkspaceName(workspaceURL string) string {
	parsedURL, err := url.Parse(workspaceURL)
	if err != nil {
		return ""
	}

	// Extract subdomain from hostname (e.g., myworkspace.us1.alpacon.io -> myworkspace)
	hostname := parsedURL.Hostname()
	parts := strings.Split(hostname, ".")
	if len(parts) > 0 {
		return parts[0]
	}

	return ""
}

func SplitAndParseInt(input string) []int {
	var intValues []int

	for stringValue := range strings.SplitSeq(input, ",") {
		trimmedString := strings.TrimSpace(stringValue)

		intValue, err := strconv.Atoi(trimmedString)
		if err != nil {
			CliErrorWithExit("Invalid input: only integers allowed.")
		}
		intValues = append(intValues, intValue)
	}

	return intValues
}

func TimeUtils(t time.Time) string {
	if t.IsZero() {
		return "None"
	}

	now := time.Now()
	diff := t.Sub(now)

	if diff >= 0 {
		switch {
		case diff < time.Minute:
			return "in a few seconds"
		case diff < time.Hour:
			return fmt.Sprintf("in %d minutes", diff/time.Minute)
		case diff < 24*time.Hour:
			return fmt.Sprintf("in %d hours", diff/time.Hour)
		case diff < 48*time.Hour:
			return "tomorrow"
		default:
			return fmt.Sprintf("in %d days", diff/(24*time.Hour))
		}
	} else {
		diff = -diff
		switch {
		case diff < time.Minute:
			return "just now"
		case diff < time.Hour:
			return fmt.Sprintf("%d minutes ago", diff/time.Minute)
		case diff < 24*time.Hour:
			return fmt.Sprintf("%d hours ago", diff/time.Hour)
		case diff < 48*time.Hour:
			return "yesterday"
		default:
			return fmt.Sprintf("%d days ago", diff/(24*time.Hour))
		}
	}
}

func TimeFormat(value int) *string {
	expiresAt := time.Now().Add(time.Hour * 24 * time.Duration(value))
	formattedExpiresAt := expiresAt.Format(time.RFC3339)

	return &formattedExpiresAt
}

func TruncateString(str string, num int) string {
	runes := []rune(str)
	if len(runes) > num {
		return string(runes[:num]) + "..."
	}
	return str
}

func RemovePrefixBeforeAPI(url string) string {
	apiIndex := strings.Index(url, "/api/")
	if apiIndex == -1 {
		return url
	}
	return url[apiIndex:]
}

func DeleteFile(path string) error {
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}

	if info.IsDir() {
		return os.RemoveAll(path)
	}

	return os.Remove(path)
}

// CleanupTempFile closes f and removes the backing file. Safe to call with
// a nil pointer. Intended as the cleanup half of SpoolToTempFile.
func CleanupTempFile(f *os.File) {
	if f == nil {
		return
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
}

// SpoolToTempFile creates a temp file, invokes fn to write into it, closes it
// to surface flush errors, then reopens it for reading with its size. On any
// error the temp file is closed and removed. The caller owns cleanup on
// success.
func SpoolToTempFile(pattern string, fn func(io.Writer) error) (*os.File, int64, error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return nil, 0, err
	}
	name := f.Name()

	if err := fn(f); err != nil {
		CleanupTempFile(f)
		return nil, 0, err
	}

	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return nil, 0, err
	}

	info, err := os.Stat(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, 0, err
	}

	readFile, err := os.Open(name)
	if err != nil {
		_ = os.Remove(name)
		return nil, 0, err
	}

	return readFile, info.Size(), nil
}

func ZipToWriter(folderPath string, w io.Writer) error {
	zipWriter := zip.NewWriter(w)
	folderName := filepath.Base(folderPath)

	err := filepath.Walk(folderPath, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}

		if path == folderPath {
			return nil
		}

		relPath, err := filepath.Rel(folderPath, path)
		if err != nil {
			return err
		}

		zipPath := filepath.Join(folderName, relPath)
		zipPath = filepath.ToSlash(zipPath)

		if info.IsDir() {
			zipPath += "/"
		}

		header, err := zip.FileInfoHeader(info)
		if err != nil {
			return err
		}
		header.Name = zipPath

		if !info.IsDir() {
			header.Method = zip.Deflate
		}

		writer, err := zipWriter.CreateHeader(header)
		if err != nil {
			return err
		}

		if !info.IsDir() {
			return writeFileToZip(writer, path)
		}

		return nil
	})

	if err != nil {
		_ = zipWriter.Close()
		return err
	}

	err = zipWriter.Close()
	if err != nil {
		return err
	}

	return nil
}

func writeFileToZip(writer io.Writer, path string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}

	_, copyErr := io.Copy(writer, file)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}

func Unzip(src string, dest string) error {
	r, err := zip.OpenReader(src)
	if err != nil {
		return err
	}
	defer func() { _ = r.Close() }()

	// Absolute so a relative dest such as "." keeps the prefix filepath.Join cleans away.
	// CodeQL's go/zipslip accepts only this prefix form; TrimSuffix keeps a root dest off "//".
	destDir, err := filepath.Abs(dest)
	if err != nil {
		return err
	}
	destPrefix := strings.TrimSuffix(destDir, string(os.PathSeparator)) + string(os.PathSeparator)

	for _, f := range r.File {
		// Prevent zip slip vulnerability by validating file path
		// Reject absolute paths
		if filepath.IsAbs(f.Name) {
			return fmt.Errorf("invalid file path (absolute path): %s", f.Name)
		}

		fpath := filepath.Join(destDir, f.Name)

		if !strings.HasPrefix(fpath, destPrefix) {
			return fmt.Errorf("invalid file path: %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			err := os.MkdirAll(fpath, os.ModePerm)
			if err != nil {
				return err
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(fpath), os.ModePerm); err != nil {
			return err
		}

		if err := extractFile(fpath, f); err != nil {
			return err
		}
	}
	return nil
}

func extractFile(fpath string, f *zip.File) (err error) {
	outFile, err := os.OpenFile(fpath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
	if err != nil {
		return err
	}
	defer func() {
		if cerr := outFile.Close(); cerr != nil && err == nil {
			err = cerr
		}
	}()

	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer func() { _ = rc.Close() }()

	_, err = io.Copy(outFile, rc)
	return err
}

func BoolPointerToString(value *bool) string {
	if value == nil {
		return "null"
	}
	if *value {
		return "true"
	}
	return "false"
}

func BuildURL(basePath, relativePath string, params map[string]string) string {
	u, err := url.Parse(basePath)
	if err != nil {
		CliErrorWithExit("Failed to parse base URL")
	}

	u.Path = path.Join(u.Path, relativePath)
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	q := u.Query()

	for key, value := range params {
		q.Set(key, value)
	}

	u.RawQuery = q.Encode()
	return u.String()
}

func IsUUID(str string) bool {
	return uuidRegex.MatchString(str)
}

func StripANSIEscapes(s string) string {
	return ansiEscapeRE.ReplaceAllString(s, "")
}

// IsControlRune reports whether r is a C0, DEL, or C1 control character (the last can introduce CSI/OSC on 8-bit terminals).
func IsControlRune(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f)
}

// IsC1OrDEL reports whether r is DEL or a C1 control. A terminal decodes U+009B as
// an 8-bit CSI, so these open a control sequence that the escape pass never sees:
// it matches ESC-led forms alone.
func IsC1OrDEL(r rune) bool {
	return r == 0x7f || (r >= 0x80 && r <= 0x9f)
}

func StripControlChars(s string) string {
	return strings.Map(func(r rune) rune {
		if IsControlRune(r) {
			return -1
		}
		return r
	}, s)
}

// StripFormatChars removes the Unicode format (Cf) characters. No Cf rune is a
// control byte, so IsControlRune misses them all, while the bidi controls among
// them reorder what a terminal renders: "denied" can display as "approved".
func StripFormatChars(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.Is(unicode.Cf, r) {
			return -1
		}
		return r
	}, s)
}

// StripFormatAndANSI removes format characters before escape sequences. Reversed,
// one buried in a sequence would break the match and leave its tail on screen.
func StripFormatAndANSI(s string) string {
	return StripANSIEscapes(StripFormatChars(s))
}

// SanitizeTerminalText strips escape sequences before the standalone control
// bytes; reversed, ESC alone would go and "[2K" would stay on screen as text.
func SanitizeTerminalText(s string) string {
	return StripControlChars(StripFormatAndANSI(s))
}

// SanitizeTerminalLine is SanitizeTerminalText plus the answer to whether it had
// to take anything out. A \n counts as altered here: the caller's value is one
// line by contract, so a newline in it is a forged line and not a line ending
// worth keeping.
func SanitizeTerminalLine(s string) (clean string, altered bool) {
	clean = SanitizeTerminalText(s)
	return clean, clean != s
}

// SanitizeTerminalBlock sanitizes per line, because SanitizeTerminalText drops
// \n and callers print multi-line values the operator saves as files. CRLF is
// folded first, or a server sending DOS line endings would report every value
// as altered; a lone \r is still dropped, and does report altered.
func SanitizeTerminalBlock(s string) (clean string, altered bool) {
	normalized := strings.ReplaceAll(s, "\r\n", "\n")
	lines := strings.Split(normalized, "\n")
	for i, line := range lines {
		lines[i] = SanitizeTerminalText(line)
	}
	clean = strings.Join(lines, "\n")
	return clean, clean != normalized
}

// ProcessEditedData facilitates user modifications to original data,
// formats it, supports editing via a temp file, compares the edited data against the original,
// and parses it into JSON. If no changes are made, the update is aborted and an error is returned.
func ProcessEditedData(originalData []byte) (any, error) {
	prettyJSON, err := PrettyJSON(originalData)
	if err != nil {
		return nil, err
	}

	tmpFile, err := CreateAndEditTempFile(prettyJSON.Bytes())
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.Remove(tmpFile) }()

	editedContent, err := os.ReadFile(tmpFile)
	if err != nil {
		return nil, err
	}

	if bytes.Equal(prettyJSON.Bytes(), editedContent) {
		CliInfoWithExit("No changes made. Aborting update.")
	}

	var jsonData any
	err = json.Unmarshal(editedContent, &jsonData)
	if err != nil {
		return nil, err
	}

	return jsonData, nil
}

func CreateAndEditTempFile(data []byte) (string, error) {
	tmpl, err := os.CreateTemp("", "example.*.json")
	if err != nil {
		return "", errors.New("failed to create temp file for update")
	}
	defer func() { _ = tmpl.Close() }()

	if _, err = tmpl.Write(data); err != nil {
		return "", err
	}

	if err = tmpl.Close(); err != nil {
		return "", err
	}

	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	cmd := exec.Command(editor, tmpl.Name())
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	if err = cmd.Run(); err != nil {
		return "", err
	}

	return tmpl.Name(), nil
}

// SplitPath splits a "server:/path" target into its server name and remote path.
// It returns an error when path carries no ':' separator or no server name.
// An empty remote path is accepted; callers decide whether it is meaningful.
func SplitPath(path string) (string, string, error) {
	server, remotePath, found := strings.Cut(path, ":")
	if !found {
		return "", "", fmt.Errorf("invalid remote path %q: missing ':' separator", path)
	}
	if server == "" {
		return "", "", fmt.Errorf("invalid remote path %q: missing server name", path)
	}
	return server, remotePath, nil
}

// IsInteractiveShell checks if the current program is running in an interactive terminal.
func IsInteractiveShell() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

// CompactStrings trims whitespace from each element and drops empty entries.
func CompactStrings(ss []string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// SplitAndTrim splits s by sep, trims whitespace from each element, and drops empty entries.
// Returns nil when the input is empty or yields no non-empty elements.
func SplitAndTrim(s, sep string) []string {
	if s == "" {
		return nil
	}
	parts := strings.Split(s, sep)
	result := make([]string, 0, len(parts))
	for _, p := range parts {
		if trimmed := strings.TrimSpace(p); trimmed != "" {
			result = append(result, trimmed)
		}
	}
	if len(result) == 0 {
		return nil
	}
	return result
}

// RequirePositiveInt exits with a usage error when an integer flag is not positive.
func RequirePositiveInt(flagName string, value int) {
	if value <= 0 {
		CliErrorWithExitCode(ExitCodeUsageError, "--%s must be a positive integer.", flagName)
	}
}

// ParsePositiveDuration parses a duration flag value, rejecting non-positive ones.
// It trims surrounding whitespace so every duration flag normalizes input the same way.
func ParsePositiveDuration(flagName, raw string) (time.Duration, error) {
	raw = strings.TrimSpace(raw)
	d, err := time.ParseDuration(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid %s value %q: %w", flagName, raw, err)
	}
	if d <= 0 {
		return 0, fmt.Errorf("invalid %s value %q: must be a positive duration", flagName, raw)
	}
	return d, nil
}
