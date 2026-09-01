package selfupdate

import (
	"strconv"
	"strings"

	"github.com/alpacax/alpacon-cli/utils"
)

// IsUnknownVersion answers for a build this CLI cannot place against a release:
// the literal dev build, and anything whose first part is not a number. Both
// sort as 0.0.0—compareCore reads an unreadable part as zero—so without this
// they would pull the latest release down on top of themselves on every run.
func IsUnknownVersion(version string) bool {
	if version == utils.DevVersion {
		return true
	}
	core, _ := splitPrerelease(normalizeVersion(version))
	first, _, _ := strings.Cut(core, ".")
	_, err := strconv.Atoi(first)
	return err != nil
}

// normalizeVersion drops the leading v a tag may carry. LatestRelease already
// strips it from the release side, so leaving it on the running version would
// make a build stamped v1.4.0 reinstall 1.4.0 every time it is asked.
func normalizeVersion(version string) string {
	return strings.TrimPrefix(version, "v")
}

// IsOutdated only ever moves forward. The release workflow publishes a
// pre-release tag with --latest=false, so /releases/latest answers an rc build
// with the previous stable release; treating any difference as outdated would
// install that older binary and report it as an update.
func IsOutdated(current, latest string) bool {
	return compareVersions(current, latest) < 0
}

// A part that is not a number counts as zero, so a tag this CLI cannot read
// compares equal rather than newer: the update declines instead of installing
// something it could not place.
func compareVersions(a, b string) int {
	aCore, aPre := splitPrerelease(normalizeVersion(a))
	bCore, bPre := splitPrerelease(normalizeVersion(b))
	if order := compareCore(aCore, bCore); order != 0 {
		return order
	}

	switch {
	case aPre == bPre:
		return 0
	case aPre == "":
		return 1
	case bPre == "":
		return -1
	}
	return comparePrerelease(aPre, bPre)
}

func comparePrerelease(a, b string) int { // Lexically, rc10 sorts below rc2, so an rc series would stop advancing at its tenth build.
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) && i < len(bParts); i++ {
		if order := compareIdentifier(aParts[i], bParts[i]); order != 0 {
			return order
		}
	}
	return compareInt(len(aParts), len(bParts))
}

func compareIdentifier(a, b string) int {
	aText, aNumber := splitTrailingNumber(a)
	bText, bNumber := splitTrailingNumber(b)
	if order := strings.Compare(aText, bText); order != 0 {
		return order
	}
	return compareInt(aNumber, bNumber)
}

func splitTrailingNumber(identifier string) (string, int) {
	digits := len(identifier)
	for digits > 0 && identifier[digits-1] >= '0' && identifier[digits-1] <= '9' {
		digits--
	}
	number, err := strconv.Atoi(identifier[digits:])
	if err != nil {
		return identifier, -1
	}
	return identifier[:digits], number
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func compareCore(a, b string) int {
	aParts := strings.Split(a, ".")
	bParts := strings.Split(b, ".")
	for i := 0; i < len(aParts) || i < len(bParts); i++ {
		if order := compareInt(numberAt(aParts, i), numberAt(bParts, i)); order != 0 {
			return order
		}
	}
	return 0
}

func numberAt(parts []string, index int) int {
	if index >= len(parts) {
		return 0
	}
	number, err := strconv.Atoi(parts[index])
	if err != nil || number < 0 {
		return 0
	}
	return number
}

func splitPrerelease(version string) (string, string) {
	version, _, _ = strings.Cut(version, "+")
	core, prerelease, _ := strings.Cut(version, "-")
	return core, prerelease
}
