package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	osexec "os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoolPointerToString(t *testing.T) {
	t.Parallel()
	trueVal := true
	falseVal := false

	assert.Equal(t, "null", BoolPointerToString(nil))
	assert.Equal(t, "true", BoolPointerToString(&trueVal))
	assert.Equal(t, "false", BoolPointerToString(&falseVal))
}

func TestTruncateString(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		str      string
		num      int
		expected string
	}{
		{"longer than limit", "hello world", 5, "hello..."},
		{"exactly at limit", "hello", 5, "hello"},
		{"shorter than limit", "hi", 10, "hi"},
		{"empty string", "", 5, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TruncateString(tt.str, tt.num))
		})
	}
}

func TestIsUUID(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"valid UUID v4", "550e8400-e29b-41d4-a716-446655440000", true},
		{"valid UUID uppercase", "550E8400-E29B-41D4-A716-446655440000", true},
		{"plain name", "my-server", false},
		{"empty string", "", false},
		{"partial UUID", "550e8400-e29b-41d4", false},
		{"UUID without dashes", "550e8400e29b41d4a716446655440000", true}, // uuid.Parse accepts 32-char hex
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, IsUUID(tt.input))
		})
	}
}

func TestBuildURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		basePath     string
		relativePath string
		params       map[string]string
		wantSuffix   string
	}{
		{"base only", "/api/servers/servers/", "", nil, "/api/servers/servers/"},
		{"base with id", "/api/servers/servers/", "abc-123", nil, "/api/servers/servers/abc-123/"},
		{"base with params", "/api/servers/servers/", "", map[string]string{"name": "my-server"}, "/api/servers/servers/?name=my-server"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := BuildURL(tt.basePath, tt.relativePath, tt.params)
			assert.Contains(t, result, tt.wantSuffix)
		})
	}
}

func TestTimeUtils(t *testing.T) {
	t.Parallel()
	now := time.Now()

	tests := []struct {
		name     string
		input    time.Time
		expected string
	}{
		{"zero time", time.Time{}, "None"},
		{"30 seconds ago", now.Add(-30 * time.Second), "just now"},
		{"5 minutes ago", now.Add(-5 * time.Minute), "5 minutes ago"},
		{"3 hours ago", now.Add(-3 * time.Hour), "3 hours ago"},
		{"yesterday", now.Add(-30 * time.Hour), "yesterday"},
		{"3 days ago", now.Add(-72 * time.Hour), "3 days ago"},
		// Future cases use a buffer (e.g. +30s, +30m) to guard against integer
		// truncation: time.Duration division truncates toward zero, so
		// 5m0s becomes 4m59s by the time it reaches the threshold check.
		// Past cases don't need a buffer because they are already in the past.
		{"in a few seconds", now.Add(30 * time.Second), "in a few seconds"},
		{"in 5 minutes", now.Add(5*time.Minute + 30*time.Second), "in 5 minutes"},
		{"in 3 hours", now.Add(3*time.Hour + 30*time.Minute), "in 3 hours"},
		{"tomorrow", now.Add(30 * time.Hour), "tomorrow"},
		{"in 3 days", now.Add(72*time.Hour + 30*time.Minute), "in 3 days"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, TimeUtils(tt.input))
		})
	}
}

func TestExtractWorkspaceName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"standard URL", "https://myws.us1.alpacon.io", "myws"},
		{"no subdomain", "https://alpacon.io", "alpacon"},
		{"empty string", "", ""},
		{"localhost", "http://localhost:8000", "localhost"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, ExtractWorkspaceName(tt.input))
		})
	}
}

func TestRemovePrefixBeforeAPI(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"full URL", "https://example.com/api/servers/", "/api/servers/"},
		{"already relative", "/api/test/", "/api/test/"},
		{"no /api/", "no-api-here", "no-api-here"},
		{"api in middle", "prefix/api/resource/", "/api/resource/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, RemovePrefixBeforeAPI(tt.input))
		})
	}
}

func TestSaveStream(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "nested", "file.txt")

	written, err := saveStream(dest, strings.NewReader("hello world"))
	require.NoError(t, err)
	assert.Equal(t, int64(len("hello world")), written)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "hello world", string(content))
}

type failingReader struct {
	reader *strings.Reader
}

func (r *failingReader) Read(p []byte) (int, error) {
	if r.reader.Len() > 0 {
		return r.reader.Read(p)
	}
	return 0, io.ErrUnexpectedEOF
}

func TestSaveStreamAtomic_RetainsExistingFileOnReadError(t *testing.T) {
	t.Parallel()
	dest := filepath.Join(t.TempDir(), "nested", "file.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0755))
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0644))

	written, err := SaveStreamAtomic(dest, &failingReader{reader: strings.NewReader("partial")}, 0600)
	require.Error(t, err)
	assert.Equal(t, int64(len("partial")), written)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content))

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dest), ".alpacon-*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func requireUnixModes(t *testing.T) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("Unix file modes are not enforced on Windows")
	}
}

func TestSaveStreamAtomic_PreservesExistingFileMode(t *testing.T) {
	t.Parallel()
	requireUnixModes(t)

	dest := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0600))

	written, err := SaveStreamAtomic(dest, strings.NewReader("replacement"), 0666)
	require.NoError(t, err)
	assert.Equal(t, int64(len("replacement")), written)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "replacement", string(content))

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestSaveStreamAtomic_WarnsWhenKeptModeIsWiderThanANewFile(t *testing.T) {
	requireUnixModes(t)

	dest := filepath.Join(t.TempDir(), "id_rsa")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0644))
	// Chmod because a restrictive umask would mask the WriteFile mode down to
	// owner-only, and the warning would never fire.
	require.NoError(t, os.Chmod(dest, 0644))

	stderr := captureStderr(t, func() {
		_, err := SaveStreamAtomic(dest, strings.NewReader("key"), 0600)
		require.NoError(t, err)
	})

	assert.Contains(t, stderr, "already existed at 0644 and kept that mode")
	assert.Contains(t, stderr, "at most 0600")
}

// TestKeptModeIsWider pins the guard directly: driving the same branches
// through the filesystem makes them umask-dependent, and a umask that narrows
// the setup mode retires the branch instead of testing it.
func TestKeptModeIsWider(t *testing.T) {
	t.Parallel()
	requireUnixModes(t)

	tests := []struct {
		name        string
		keptPerm    os.FileMode
		newFilePerm os.FileMode
		want        bool
	}{
		{"private key over a group-readable file", 0644, 0600, true},
		{"destination already owner-only", 0600, 0600, false},
		{"a new file would be just as readable", 0644, 0666, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, keptModeIsWider(tt.keptPerm, tt.newFilePerm))
		})
	}
}

func TestSaveStreamAtomic_NoWarningForANewFile(t *testing.T) {
	requireUnixModes(t)

	stderr := captureStderr(t, func() {
		_, err := SaveStreamAtomic(filepath.Join(t.TempDir(), "fresh"), strings.NewReader("x"), 0600)
		require.NoError(t, err)
	})

	assert.Empty(t, stderr)
}

// tempModeReader records the mode of every temp file present in dir each time
// the stream is read, so a test can see what the partial file was exposed as.
type tempModeReader struct {
	reader io.Reader
	dir    string
	modes  []os.FileMode
}

func (r *tempModeReader) Read(p []byte) (int, error) {
	matches, err := filepath.Glob(filepath.Join(r.dir, ".alpacon-*.tmp"))
	if err != nil {
		return 0, err
	}
	for _, match := range matches {
		info, err := os.Stat(match)
		if err != nil {
			return 0, err
		}
		r.modes = append(r.modes, info.Mode().Perm())
	}
	return r.reader.Read(p)
}

func TestSaveStreamAtomic_KeepsPartialReplacementUnreadable(t *testing.T) {
	t.Parallel()
	requireUnixModes(t)

	dir := t.TempDir()
	dest := filepath.Join(dir, "file.txt")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0644))
	require.NoError(t, os.Chmod(dest, 0644))

	// The stream dies after its first chunk, so the modes sampled below are the
	// ones a partial replacement actually sat at.
	reader := &tempModeReader{reader: &failingReader{reader: strings.NewReader("replacement")}, dir: dir}
	_, err := SaveStreamAtomic(dest, reader, 0600)
	require.Error(t, err)

	require.NotEmpty(t, reader.modes)
	for _, mode := range reader.modes {
		assert.Zero(t, mode&0077)
	}

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content))

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0644), info.Mode().Perm())
}

func TestSaveStreamAtomic_AppliesRequestedModeToNewFile(t *testing.T) {
	t.Parallel()
	requireUnixModes(t)

	dest := filepath.Join(t.TempDir(), "file.txt")

	written, err := SaveStreamAtomic(dest, strings.NewReader("created"), 0600)
	require.NoError(t, err)
	assert.Equal(t, int64(len("created")), written)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "created", string(content))

	// The umask can narrow 0600 further, so pin the guarantee that holds: no
	// group or other bits. The successful read above covers the owner side.
	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Zero(t, info.Mode().Perm()&0077)
}

func TestSaveStreamAtomic_WritesThroughExistingSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows environments")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	chain := filepath.Join(dir, "chain.txt")
	require.NoError(t, os.WriteFile(target, []byte("existing"), 0644))
	require.NoError(t, os.Symlink(target, link))
	require.NoError(t, os.Symlink("link.txt", chain))

	_, err := SaveStreamAtomic(chain, strings.NewReader("replacement"), 0600)
	require.NoError(t, err)

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "replacement", string(content))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
	info, err = os.Lstat(chain)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestSaveStreamAtomic_WritesThroughDanglingSymlink(t *testing.T) {
	t.Parallel()
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows environments")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink("target.txt", link))

	_, err := SaveStreamAtomic(link, strings.NewReader("created"), 0600)
	require.NoError(t, err)

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "created", string(content))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestSpoolToTempFile_ReopensForReadingAndReportsSize(t *testing.T) {
	t.Parallel()
	f, size, err := SpoolToTempFile("alpacon-spool-success-*.tmp", func(w io.Writer) error {
		_, err := w.Write([]byte("spooled"))
		return err
	})
	require.NoError(t, err)
	defer CleanupTempFile(f)

	assert.Equal(t, int64(len("spooled")), size)
	content, err := io.ReadAll(f)
	require.NoError(t, err)
	assert.Equal(t, "spooled", string(content))
}

func TestSpoolToTempFile_CleansUpOnCallbackError(t *testing.T) {
	t.Parallel()
	pattern := "alpacon-spool-cleanup-" + strings.ReplaceAll(t.Name(), "/", "-") + "-*.tmp"
	wantErr := errors.New("spool failed")

	f, size, err := SpoolToTempFile(pattern, func(w io.Writer) error {
		_, writeErr := w.Write([]byte("partial"))
		require.NoError(t, writeErr)
		return wantErr
	})
	require.ErrorIs(t, err, wantErr)
	assert.Nil(t, f)
	assert.Zero(t, size)

	matches, globErr := filepath.Glob(filepath.Join(os.TempDir(), pattern))
	require.NoError(t, globErr)
	assert.Empty(t, matches)
}

func TestZipToWriter(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "file.txt"), []byte("hello"), 0644))
	require.NoError(t, os.Mkdir(filepath.Join(root, "nested"), 0755))
	require.NoError(t, os.WriteFile(filepath.Join(root, "nested", "child.txt"), []byte("world"), 0644))

	var buf bytes.Buffer
	require.NoError(t, ZipToWriter(root, &buf))

	reader, err := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	require.NoError(t, err)

	contents := make(map[string]string)
	for _, file := range reader.File {
		if file.FileInfo().IsDir() {
			continue
		}
		rc, err := file.Open()
		require.NoError(t, err)
		data, err := io.ReadAll(rc)
		require.NoError(t, err)
		require.NoError(t, rc.Close())
		contents[file.Name] = string(data)
	}

	folderName := filepath.Base(root)
	assert.Equal(t, "hello", contents[filepath.ToSlash(filepath.Join(folderName, "file.txt"))])
	assert.Equal(t, "world", contents[filepath.ToSlash(filepath.Join(folderName, "nested", "child.txt"))])
}

func TestSplitAndTrim(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"normal", "command,websh", []string{"command", "websh"}},
		{"whitespace around values", " command , websh ", []string{"command", "websh"}},
		{"trailing comma", "command,websh,", []string{"command", "websh"}},
		{"leading comma", ",command,websh", []string{"command", "websh"}},
		{"empty input", "", nil},
		{"single value", "command", []string{"command"}},
		{"only commas", ",,,", nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SplitAndTrim(tt.input, ","))
		})
	}
}

func TestSplitPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		wantServer string
		wantPath   string
		wantErrMsg string // empty means no error expected
	}{
		{"server and path", "myserver:/home/user/file.txt", "myserver", "/home/user/file.txt", ""},
		{"path with colon", "myserver:/tmp/a:b", "myserver", "/tmp/a:b", ""},
		{"empty path after colon", "myserver:", "myserver", "", ""},
		{"empty server before colon", ":/tmp/file", "", "", "missing server name"},
		{"no colon", "localfile.txt", "", "", "missing ':' separator"},
		{"empty input", "", "", "", "missing ':' separator"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server, path, err := SplitPath(tt.input)
			if tt.wantErrMsg != "" {
				assert.ErrorContains(t, err, tt.wantErrMsg)
				assert.ErrorContains(t, err, fmt.Sprintf("%q", tt.input))
			} else {
				require.NoError(t, err)
			}
			assert.Equal(t, tt.wantServer, server)
			assert.Equal(t, tt.wantPath, path)
		})
	}
}

func TestStripANSIEscapes(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"CSI color codes", "\x1b[31mfoo\x1b[0m", "foo"},
		{"CSI erase line", "abc\x1b[2Kdef", "abcdef"},
		{"OSC BEL-terminated window title", "\x1b]0;title\x07bar", "bar"},
		{"OSC ST-terminated window title", "\x1b]0;title\x1b\\baz", "baz"},
		{"DCS string terminated by ST", "\x1bPq123\x1b\\qux", "qux"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, StripANSIEscapes(tt.input))
		})
	}
}

func TestStripControlChars(t *testing.T) {
	t.Parallel()
	// C0, DEL, and C1 (rune 0x85 = NEL) are removed; printable Unicode (é = 0xE9) is kept.
	assert.Equal(t, "abc", StripControlChars("a\x00b\x7fc"))
	assert.Equal(t, "ab", StripControlChars("a\u0085b"))
	assert.Equal(t, "abé", StripControlChars("a\u0085bé"))
}

func TestStripFormatChars(t *testing.T) {
	t.Parallel()
	// Cf carries no control byte, so the control passes leave it behind.
	assert.Equal(t, "denied approved", StripFormatChars("denied\u202e\u2066 approved"))
	assert.Equal(t, "abé", StripFormatChars("a\u200dbé"))

	// The bidi controls are the ones that matter: they reorder what a terminal
	// renders without changing a byte. All twelve members of Bidi_Control go.
	for _, r := range []rune{
		0x202a, 0x202b, 0x202c, 0x202d, 0x202e, // embedding and override
		0x2066, 0x2067, 0x2068, 0x2069, // isolates
		0x061c, 0x200e, 0x200f, // marks
	} {
		assert.Equal(t, "ab", StripFormatChars("a"+string(r)+"b"), "U+%04X", r)
	}

	// Control bytes are StripControlChars' job; this pass leaves them alone.
	assert.Equal(t, "a\x1bb", StripFormatChars("a\x1bb"))
}

func TestSanitizeTerminalText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"bidi override", "denied\u202e\u2066 approved", "denied approved"},
		// Cf goes first, or the sequence no longer matches and "[2K" stays on screen.
		{"Cf buried in a sequence", "a\x1b[2\u200dKb", "ab"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeTerminalText(tt.input))
		})
	}
}

func TestRequirePositiveIntExitsWithUsageErrorCode(t *testing.T) {
	t.Parallel()
	helper := osexec.Command(os.Args[0], "-test.run=^TestRequirePositiveIntHelperProcess$")
	helper.Env = append(os.Environ(), "GO_WANT_REQUIRE_POSITIVE_INT_HELPER=1")

	err := helper.Run()

	var exitErr *osexec.ExitError
	require.ErrorAs(t, err, &exitErr)
	assert.Equal(t, ExitCodeUsageError, exitErr.ExitCode())
}

func TestRequirePositiveIntHelperProcess(t *testing.T) {
	t.Parallel()
	if os.Getenv("GO_WANT_REQUIRE_POSITIVE_INT_HELPER") != "1" {
		return
	}
	RequirePositiveInt("tail", 0)
}

func TestSanitizeTerminalLine(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantClean   string
		wantAltered bool
	}{
		{
			name:      "leaves a clean value alone",
			input:     "https://demo.alpacon.io/activate?code=ABCD-1234",
			wantClean: "https://demo.alpacon.io/activate?code=ABCD-1234",
		},
		{
			name:        "drops an injected newline and reports it",
			input:       "https://demo.alpacon.io\n\nVerification code: FAKE-0000",
			wantClean:   "https://demo.alpacon.ioVerification code: FAKE-0000",
			wantAltered: true,
		},
		{
			name:        "drops a CRLF and reports it",
			input:       "https://demo.alpacon.io\r\nVerification code: FAKE-0000",
			wantClean:   "https://demo.alpacon.ioVerification code: FAKE-0000",
			wantAltered: true,
		},
		{
			name:        "drops an ANSI escape and reports it",
			input:       "https://demo.alpacon.io\x1b[2Khttps://evil.example.com",
			wantClean:   "https://demo.alpacon.iohttps://evil.example.com",
			wantAltered: true,
		},
		{
			name:        "drops a bidi override and reports it",
			input:       "ABCD\u202e-1234",
			wantClean:   "ABCD-1234",
			wantAltered: true,
		},
		{
			name:      "reports nothing for an empty value",
			input:     "",
			wantClean: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean, altered := SanitizeTerminalLine(tt.input)

			assert.Equal(t, tt.wantClean, clean)
			assert.Equal(t, tt.wantAltered, altered)
		})
	}
}

func TestSanitizeTerminalBlock(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		input       string
		wantClean   string
		wantAltered bool
	}{
		{
			name:      "keeps the line count of a clean block",
			input:     "[servers]\nweb-01 ansible_host=10.0.0.1",
			wantClean: "[servers]\nweb-01 ansible_host=10.0.0.1",
		},
		{
			name:      "folds CRLF without reporting a change",
			input:     "[servers]\r\nweb-01",
			wantClean: "[servers]\nweb-01",
		},
		{
			name:        "drops a lone carriage return and reports it",
			input:       "curl real.example.com\rcurl evil.example.com",
			wantClean:   "curl real.example.comcurl evil.example.com",
			wantAltered: true,
		},
		{
			name:        "drops a tab and reports it",
			input:       "hosts:\n\tweb-01",
			wantClean:   "hosts:\nweb-01",
			wantAltered: true,
		},
		{
			name:        "drops an ANSI escape and reports it",
			input:       "curl real.example.com\x1b[2Kcurl evil.example.com",
			wantClean:   "curl real.example.comcurl evil.example.com",
			wantAltered: true,
		},
		{
			name:        "drops a bidi override and reports it",
			input:       "curl \u202ereal.example.com",
			wantClean:   "curl real.example.com",
			wantAltered: true,
		},
		{
			name:      "reports nothing for an empty value",
			input:     "",
			wantClean: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clean, altered := SanitizeTerminalBlock(tt.input)

			assert.Equal(t, tt.wantClean, clean)
			assert.Equal(t, tt.wantAltered, altered)
		})
	}
}
