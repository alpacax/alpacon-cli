package kubeconfig

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
)

const envVar = "KUBECONFIG"

// ResolvePath picks the file 'kubectl config' itself would write to: the
// explicit path when one is given, then the first $KUBECONFIG entry that
// exists, else the last entry listed, and <home>/.kube/config when $KUBECONFIG
// names nothing.
func ResolvePath(flag string) (string, error) {
	if flag != "" {
		return flag, nil
	}
	if files := envFiles(); len(files) > 0 {
		for _, f := range files {
			if _, err := os.Stat(f); err == nil {
				return f, nil
			}
		}
		return files[len(files)-1], nil
	}
	home, err := homeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".kube", "config"), nil
}

// Shadowed reports whether path is a $KUBECONFIG entry listed after another
// one. kubectl takes current-context and each name from the first file that
// sets it, so what was written to path may not be what kubectl ends up using.
func Shadowed(path string) bool {
	for i, f := range envFiles() {
		if filepath.Clean(f) == filepath.Clean(path) {
			return i > 0
		}
	}
	return false
}

// envFiles splits $KUBECONFIG the way kubectl does, deduplicated in order.
// Empty entries are dropped: kubectl would treat one as a file named "".
func envFiles() []string {
	var files []string
	seen := map[string]bool{}
	for _, f := range filepath.SplitList(os.Getenv(envVar)) {
		if f == "" || seen[f] {
			continue
		}
		seen[f] = true
		files = append(files, f)
	}
	return files
}

func homeDir() (string, error) {
	if runtime.GOOS != "windows" {
		return os.UserHomeDir()
	}
	if home := windowsHomeDir(os.Getenv); home != "" {
		return home, nil
	}
	return "", errors.New("cannot find a home directory: none of HOME, USERPROFILE, or HOMEDRIVE and HOMEPATH is set")
}

// windowsHomeDir follows kubectl's home lookup on Windows, so both tools land
// on the same .kube\config:
//  1. the first of %HOME%, %HOMEDRIVE%%HOMEPATH%, %USERPROFILE% holding .kube\config
//  2. else the first of %HOME%, %USERPROFILE%, %HOMEDRIVE%%HOMEPATH% that is a writable directory
//  3. else the first of those that exists, and then the first that is set
//
// It is platform-neutral so its order can be tested anywhere.
func windowsHomeDir(getenv func(string) string) string {
	home := getenv("HOME")
	userProfile := getenv("USERPROFILE")
	homeDriveHomePath := ""
	if drive, path := getenv("HOMEDRIVE"), getenv("HOMEPATH"); drive != "" && path != "" {
		homeDriveHomePath = drive + path
	}

	for _, p := range []string{home, homeDriveHomePath, userProfile} {
		if p == "" {
			continue
		}
		if _, err := os.Stat(filepath.Join(p, ".kube", "config")); err == nil {
			return p
		}
	}

	firstSet, firstExisting := "", ""
	for _, p := range []string{home, userProfile, homeDriveHomePath} {
		if p == "" {
			continue
		}
		if firstSet == "" {
			firstSet = p
		}
		info, err := os.Stat(p)
		if err != nil {
			continue
		}
		if firstExisting == "" {
			firstExisting = p
		}
		if info.IsDir() && info.Mode().Perm()&0200 != 0 {
			return p
		}
	}
	if firstExisting != "" {
		return firstExisting
	}
	return firstSet
}
