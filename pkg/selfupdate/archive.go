package selfupdate

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var maxReleaseFileSize int64 = 512 << 20 // Nothing in a release comes near it—it stops a malformed or endless stream, not anything real.

func ExtractBinary(archivePath, binaryName, destDir string) (string, error) {
	destPath := filepath.Join(destDir, binaryName+".new")
	extract := extractFromTarGz
	if strings.HasSuffix(archivePath, ".zip") {
		extract = extractFromZip
	}
	if err := extract(archivePath, binaryName, destPath); err != nil {
		return "", err
	}
	return destPath, nil
}

func extractFromTarGz(archivePath, binaryName, destPath string) error {
	file, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	gzipReader, err := gzip.NewReader(file)
	if err != nil {
		return err
	}
	defer func() { _ = gzipReader.Close() }()

	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if header.Typeflag != tar.TypeReg || filepath.Base(header.Name) != binaryName {
			continue
		}
		return writeExtracted(destPath, reader)
	}
	return fmt.Errorf("archive %s carries no entry named %s", filepath.Base(archivePath), binaryName)
}

func extractFromZip(archivePath, binaryName, destPath string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer func() { _ = reader.Close() }()

	for _, entry := range reader.File {
		if filepath.Base(entry.Name) != binaryName || !entry.Mode().IsRegular() {
			continue
		}
		opened, err := entry.Open()
		if err != nil {
			return err
		}
		writeErr := writeExtracted(destPath, opened)
		_ = opened.Close()
		return writeErr
	}
	return fmt.Errorf("archive %s carries no entry named %s", filepath.Base(archivePath), binaryName)
}

func writeExtracted(destPath string, src io.Reader) error {
	out, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0755)
	if err != nil {
		return err
	}
	written, copyErr := io.Copy(out, io.LimitReader(src, maxReleaseFileSize+1))
	closeErr := out.Close()
	if copyErr != nil {
		return copyErr
	}
	if written > maxReleaseFileSize {
		return fmt.Errorf("archive entry %s exceeds %d bytes", filepath.Base(destPath), maxReleaseFileSize)
	}
	return closeErr
}
