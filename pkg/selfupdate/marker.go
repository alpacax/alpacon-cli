package selfupdate

import (
	"os"
	"path/filepath"
)

// install.ps1 records the installed version here and reads it instead of
// 'alpacon version', so a stale marker misdirects the next install.
const installedVersionMarker = "installed-version.txt"

// syncVersionMarker rewrites an existing marker and never creates one: a marker
// beside a binary install.ps1 never placed would claim an install it does not own.
func syncVersionMarker(installDir, version string) {
	marker := filepath.Join(installDir, installedVersionMarker)
	if _, err := os.Stat(marker); err != nil {
		return
	}
	_ = os.WriteFile(marker, []byte(version), 0600) // Best effort: the binary is already replaced, and a half-written marker reads as nothing installed, which the installer repairs.
}
