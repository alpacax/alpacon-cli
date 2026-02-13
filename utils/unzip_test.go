package utils

import (
	"archive/zip"
	"os"
	"path/filepath"
	"testing"
)

func TestUnzip_ValidFiles(t *testing.T) {
	// Create a temporary zip file with valid content
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	extractDir := filepath.Join(tmpDir, "extract")

	// Create test zip file
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)
	defer zw.Close()

	// Add a valid file
	fw, err := zw.Create("test.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Write([]byte("test content"))
	if err != nil {
		t.Fatal(err)
	}

	// Add a file in subdirectory
	fw, err = zw.Create("subdir/nested.txt")
	if err != nil {
		t.Fatal(err)
	}
	_, err = fw.Write([]byte("nested content"))
	if err != nil {
		t.Fatal(err)
	}

	zw.Close()
	zf.Close()

	// Test extraction
	err = Unzip(zipPath, extractDir)
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
			zipPath := filepath.Join(tmpDir, "test.zip")
			extractDir := filepath.Join(tmpDir, "extract")

			// Create malicious zip file
			zf, err := os.Create(zipPath)
			if err != nil {
				t.Fatal(err)
			}
			defer zf.Close()

			zw := zip.NewWriter(zf)

			// Create file header manually to bypass path validation
			header := &zip.FileHeader{
				Name:   tt.filename,
				Method: zip.Deflate,
			}
			fw, err := zw.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			_, err = fw.Write([]byte("malicious content"))
			if err != nil {
				t.Fatal(err)
			}

			zw.Close()
			zf.Close()

			// Test extraction
			err = Unzip(zipPath, extractDir)
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
	tmpDir := t.TempDir()
	zipPath := filepath.Join(tmpDir, "test.zip")
	extractDir := filepath.Join(tmpDir, "extract")

	// Create zip with directory traversal
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	defer zf.Close()

	zw := zip.NewWriter(zf)

	// Add a directory with parent path
	header := &zip.FileHeader{
		Name:   "../evil-dir/",
		Method: zip.Deflate,
	}
	header.SetMode(os.ModeDir | 0755)
	_, err = zw.CreateHeader(header)
	if err != nil {
		t.Fatal(err)
	}

	zw.Close()
	zf.Close()

	// Test extraction should fail
	err = Unzip(zipPath, extractDir)
	if err == nil {
		t.Error("Expected error for directory with path traversal, but got none")
	}
}

func TestUnzip_NonExistentFile(t *testing.T) {
	tmpDir := t.TempDir()
	err := Unzip(filepath.Join(tmpDir, "nonexistent.zip"), tmpDir)
	if err == nil {
		t.Error("Expected error for non-existent zip file, but got none")
	}
}
