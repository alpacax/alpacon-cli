package utils

import (
	"archive/zip"
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBoolPointerToString(t *testing.T) {
	trueVal := true
	falseVal := false

	assert.Equal(t, "null", BoolPointerToString(nil))
	assert.Equal(t, "true", BoolPointerToString(&trueVal))
	assert.Equal(t, "false", BoolPointerToString(&falseVal))
}

func TestTruncateString(t *testing.T) {
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
	dest := filepath.Join(t.TempDir(), "nested", "file.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0755))
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0644))

	written, err := SaveStreamAtomic(dest, &failingReader{reader: strings.NewReader("partial")})
	require.Error(t, err)
	assert.Equal(t, int64(len("partial")), written)

	content, err := os.ReadFile(dest)
	require.NoError(t, err)
	assert.Equal(t, "existing", string(content))

	matches, err := filepath.Glob(filepath.Join(filepath.Dir(dest), ".alpacon-*.tmp"))
	require.NoError(t, err)
	assert.Empty(t, matches)
}

func TestSaveStreamAtomic_PreservesExistingFileMode(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "file.txt")
	require.NoError(t, os.WriteFile(dest, []byte("existing"), 0600))

	_, err := SaveStreamAtomic(dest, strings.NewReader("replacement"))
	require.NoError(t, err)

	info, err := os.Stat(dest)
	require.NoError(t, err)
	assert.Equal(t, os.FileMode(0600), info.Mode().Perm())
}

func TestSaveStreamAtomic_WritesThroughExistingSymlink(t *testing.T) {
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

	_, err := SaveStreamAtomic(chain, strings.NewReader("replacement"))
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
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires elevated privileges on some Windows environments")
	}

	dir := t.TempDir()
	target := filepath.Join(dir, "target.txt")
	link := filepath.Join(dir, "link.txt")
	require.NoError(t, os.Symlink("target.txt", link))

	_, err := SaveStreamAtomic(link, strings.NewReader("created"))
	require.NoError(t, err)

	content, err := os.ReadFile(target)
	require.NoError(t, err)
	assert.Equal(t, "created", string(content))

	info, err := os.Lstat(link)
	require.NoError(t, err)
	assert.NotZero(t, info.Mode()&os.ModeSymlink)
}

func TestSpoolToTempFile_ReopensForReadingAndReportsSize(t *testing.T) {
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
