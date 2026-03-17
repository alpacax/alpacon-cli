package utils

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// browserDebounce is the minimum interval between browser opens.
// Multiple CLI processes hitting MFA simultaneously will only open one tab.
const browserDebounce = 30 * time.Second

// OpenBrowser attempts to open the given URL in the user's default browser.
// It silently falls back to doing nothing if the browser cannot be opened
// (e.g., headless server, SSH session, container) or if another process
// recently opened a browser (debounce).
//
// Set ALPACON_NO_BROWSER=1 to disable browser auto-open globally.
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

	// Fire and forget — reap the child process asynchronously to avoid zombies.
	// Release the lock if the browser fails to start or exits with an error
	// (e.g., xdg-open returns non-zero when no browser is configured).
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

// shouldOpenBrowser checks whether it makes sense to attempt opening a browser.
func shouldOpenBrowser(url string) bool {
	// Only open http/https URLs
	if !strings.HasPrefix(url, "https://") && !strings.HasPrefix(url, "http://") {
		return false
	}

	// Respect explicit opt-out via environment variable
	if v, ok := os.LookupEnv("ALPACON_NO_BROWSER"); ok && !strings.EqualFold(v, "0") && !strings.EqualFold(v, "false") {
		return false
	}

	// Skip on SSH sessions
	if _, ok := os.LookupEnv("SSH_CONNECTION"); ok {
		return false
	}
	if _, ok := os.LookupEnv("SSH_TTY"); ok {
		return false
	}

	// On Linux, skip if no display server is available (headless/container)
	if runtime.GOOS == "linux" {
		display, hasDisplay := os.LookupEnv("DISPLAY")
		_, hasWayland := os.LookupEnv("WAYLAND_DISPLAY")
		if (!hasDisplay || display == "") && !hasWayland {
			return false
		}
	}

	return true
}

// browserLockPathFunc is the function used to resolve the lock file path.
// Overridden in tests.
var browserLockPathFunc = browserLockPath

// acquireBrowserLock checks a timestamp file to prevent multiple CLI processes
// from opening the browser simultaneously. Returns true if this process should
// open the browser, false if another process opened it recently.
func acquireBrowserLock() bool {
	lockPath := browserLockPathFunc()
	if lockPath == "" {
		return true
	}

	info, err := os.Stat(lockPath)
	if err == nil && time.Since(info.ModTime()) < browserDebounce {
		// Another process opened the browser recently
		return false
	}

	// Touch the lock file
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

// browserLockPath returns the path to the browser debounce lock file.
func browserLockPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".alpacon", ".browser_lock")
}
