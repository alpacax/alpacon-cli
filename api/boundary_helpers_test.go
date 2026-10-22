package api

import (
	"go/ast"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

func nonTestGoFiles(patterns ...string) (map[string][]string, error) {
	args := append([]string{"list", "-f", "{{.ImportPath}}\t{{.Dir}}{{range .GoFiles}}\t{{.}}{{end}}"}, patterns...)
	out, err := exec.Command("go", args...).Output()
	if err != nil {
		return nil, err
	}
	wd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	pkgs := map[string][]string{}
	for line := range strings.Lines(string(out)) {
		fields := strings.Split(strings.TrimSpace(line), "\t")
		dir, err := filepath.Rel(wd, fields[1])
		if err != nil {
			return nil, err
		}
		for _, name := range fields[2:] {
			pkgs[fields[0]] = append(pkgs[fields[0]], filepath.Join(dir, name))
		}
	}
	return pkgs, nil
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
