package api

import (
	"go/ast"
	"io/fs"
	"path/filepath"
	"strconv"
	"strings"
)

func nonTestGoFiles() (map[string][]string, error) {
	dirs := map[string][]string{}
	err := filepath.WalkDir(".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(path, ".go") && !strings.HasSuffix(path, "_test.go") {
			dirs[filepath.Dir(path)] = append(dirs[filepath.Dir(path)], path)
		}
		return nil
	})
	return dirs, err
}

func importName(file *ast.File, pkgPath string) string {
	for _, spec := range file.Imports {
		path, err := strconv.Unquote(spec.Path.Value)
		if err != nil || path != pkgPath {
			continue
		}
		if spec.Name != nil {
			return spec.Name.Name
		}
		return filepath.Base(pkgPath)
	}
	return ""
}
