package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// browserDebounce spaces browser opens so CLI processes that hit MFA at once open one tab.
const browserDebounce = 30 * time.Second

// OpenBrowser opens url in the user's default browser. It does nothing when the browser cannot be
// opened (headless server, SSH session, container) or another process opened one recently.
//
// Set ALPACON_NO_BROWSER=1 to disable it.
func OpenBrowser(url string) {
	if !shouldOpenBrowser(url) {
		return
	}

	if !acquireBrowserLock() {
		return
	}

	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "linux":
		cmd = exec.Command("xdg-open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return
	}

	// Release the lock if the browser fails to start or exits non-zero (xdg-open with no browser
	// configured); waiting in a goroutine also reaps the child.
	if err := cmd.Start(); err == nil {
		go func() {
			if err := cmd.Wait(); err != nil {
				releaseBrowserLock()
			}
		}()
	} else {
		releaseBrowserLock()
	}
}

func shouldOpenBrowser(url string) bool {
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return false
	}

	if v, ok := os.LookupEnv("ALPACON_NO_BROWSER"); ok && !strings.EqualFold(v, "0") && !strings.EqualFold(v, "false") {
		return false
	}

	if _, ok := os.LookupEnv("SSH_CONNECTION"); ok {
		return false
	}
	if _, ok := os.LookupEnv("SSH_TTY"); ok {
		return false
	}

	if runtime.GOOS == "linux" {
		display, hasDisplay := os.LookupEnv("DISPLAY")
		_, hasWayland := os.LookupEnv("WAYLAND_DISPLAY")
		if (!hasDisplay || display == "") && !hasWayland {
			return false
		}
	}

	return true
}

// browserLockPathFunc is a test seam for browserLockPath.
var browserLockPathFunc = browserLockPath

// acquireBrowserLock reports whether this process may open the browser: false when another process
// touched the lock file within browserDebounce.
func acquireBrowserLock() bool {
	lockPath := browserLockPathFunc()
	if lockPath == "" {
		return true
	}

	info, err := os.Stat(lockPath)
	if err == nil && time.Since(info.ModTime()) < browserDebounce {
		return false
	}

	if err := os.MkdirAll(filepath.Dir(lockPath), 0700); err != nil {
		return true
	}
	f, err := os.Create(lockPath)
	if err != nil {
		return true
	}
	_ = f.Close()
	return true
}

// releaseBrowserLock removes the lock file so the next attempt can retry.
func releaseBrowserLock() {
	lockPath := browserLockPathFunc()
	if lockPath != "" {
		_ = os.Remove(lockPath)
	}
}

func browserLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alpacon", ".browser_lock")
}
