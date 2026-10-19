package api

import (
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func nonTestGoFiles() (map[string][]string, error) {
	out, err := exec.Command("go", "list", "-f", "{{.Dir}}{{range .GoFiles}}\t{{.}}{{end}}", "./...").Output()
	if err != nil {
		return nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	dirs := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		dir, err := filepath.Rel(wd, fields[0])
		if err != nil {
			return nil, err
		}
		for _, name := range fields[1:] {
			dirs[dir] = append(dirs[dir], filepath.Join(dir, name))
		}
	}
	return dirs, nil
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
