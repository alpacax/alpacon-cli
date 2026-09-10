package utils

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestUnzip_ValidFiles(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "extract")
	zipPath := writeUnzipArchive(t, tmpDir, func(zw *zip.Writer) {
		for _, entry := range []struct{ name, content string }{
			{"test.txt", "test content"},
			{"subdir/nested.txt", "nested content"},
		} {
			fw, err := zw.Create(entry.name)
			require.NoError(t, err)
			_, err = fw.Write([]byte(entry.content))
			require.NoError(t, err)
		}
	})

	// Test extraction
	err := Unzip(zipPath, extractDir)
	if err != nil {
		t.Errorf("Unzip failed for valid archive: %v", err)
	}

	// Verify files were extracted
	if _, err := os.Stat(filepath.Join(extractDir, "test.txt")); os.IsNotExist(err) {
		t.Error("Expected file test.txt was not extracted")
	}
	if _, err := os.Stat(filepath.Join(extractDir, "subdir", "nested.txt")); os.IsNotExist(err) {
		t.Error("Expected file subdir/nested.txt was not extracted")
	}
}

func TestUnzip_PathTraversalAttack(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		filename string
		wantErr  bool
	}{
		{
			name:     "parent directory traversal",
			filename: "../evil.txt",
			wantErr:  true,
		},
		{
			name:     "multiple parent directory traversal",
			filename: "../../../etc/passwd",
			wantErr:  true,
		},
		{
			name:     "mixed path traversal",
			filename: "safe/../../../evil.txt",
			wantErr:  true,
		},
		{
			name:     "absolute path unix",
			filename: "/etc/passwd",
			wantErr:  true,
		},
		{
			name:     "parent directory only",
			filename: "..",
			wantErr:  true,
		},
		{
			name:     "sibling directory sharing the destination prefix",
			filename: "../extract-evil/file.txt",
			wantErr:  true,
		},
		{
			name:     "valid file with dots in name",
			filename: "file..txt",
			wantErr:  false,
		},
		{
			name:     "valid subdirectory",
			filename: "subdir/file.txt",
			wantErr:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			extractDir := filepath.Join(tmpDir, "extract")

			zipPath := writeUnzipArchive(t, tmpDir, func(zw *zip.Writer) {
				// A hand-built header carries the path zw.Create would validate away.
				fw, err := zw.CreateHeader(&zip.FileHeader{Name: tt.filename, Method: zip.Deflate})
				require.NoError(t, err)
				_, err = fw.Write([]byte("malicious content"))
				require.NoError(t, err)
			})

			// Test extraction
			err := Unzip(zipPath, extractDir)
			if tt.wantErr && err == nil {
				t.Errorf("Expected error for malicious path %q, but got none", tt.filename)
			}
			if !tt.wantErr && err != nil {
				t.Errorf("Unexpected error for valid path %q: %v", tt.filename, err)
			}

			// Verify malicious file was not created outside extract directory
			if tt.wantErr {
				// Check that no files were created outside extractDir
				parentDir := filepath.Dir(extractDir)
				entries, _ := os.ReadDir(parentDir)
				for _, entry := range entries {
					if entry.Name() != "extract" && entry.Name() != "test.zip" {
						t.Errorf("File created outside extract directory: %s", entry.Name())
					}
				}
			}
		})
	}
}

func TestUnzip_DirectoryTraversal(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	extractDir := filepath.Join(tmpDir, "extract")

	zipPath := writeUnzipArchive(t, tmpDir, func(zw *zip.Writer) {
		// Add a directory with parent path
		header := &zip.FileHeader{Name: "../evil-dir/", Method: zip.Deflate}
		header.SetMode(os.ModeDir | 0755)
		_, err := zw.CreateHeader(header)
		require.NoError(t, err)
	})

	// Test extraction should fail
	if err := Unzip(zipPath, extractDir); err == nil {
		t.Error("Expected error for directory with path traversal, but got none")
	}
}

func TestUnzip_NonExistentFile(t *testing.T) {
	t.Parallel()
	tmpDir := t.TempDir()
	err := Unzip(filepath.Join(tmpDir, "nonexistent.zip"), tmpDir)
	if err == nil {
		t.Error("Expected error for non-existent zip file, but got none")
	}
}

func TestUnzip_RelativeDestination(t *testing.T) {
	tests := []struct {
		name string
		dest string
	}{
		{name: "current directory", dest: "."},
		{name: "relative subdirectory", dest: "out"},
		{name: "relative subdirectory with trailing separator", dest: "out/"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir := t.TempDir()
			zipPath := writeUnzipArchive(t, tmpDir, func(zw *zip.Writer) {
				fw, err := zw.Create("file.txt")
				require.NoError(t, err)
				_, err = fw.Write([]byte("content"))
				require.NoError(t, err)
			})

			workDir := filepath.Join(tmpDir, "work")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatal(err)
			}
			t.Chdir(workDir)

			if err := Unzip(zipPath, tt.dest); err != nil {
				t.Fatalf("Unzip into %q failed: %v", tt.dest, err)
			}
			if _, err := os.Stat(filepath.Join(workDir, tt.dest, "file.txt")); err != nil {
				t.Errorf("file.txt was not extracted into %q: %v", tt.dest, err)
			}
		})
	}
}
