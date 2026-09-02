package selfupdate

import "fmt"

// UpgradeGuidance returns text only, never a command: overwriting a file dpkg,
// rpm, or Homebrew owns desyncs that manager, and running it for the user would
// spend their root password on a command they never read.
func UpgradeGuidance(kind InstallKind) string {
	switch kind {
	case InstallHomebrew:
		return "This binary was installed by Homebrew. Update it with:\n    brew upgrade alpacon-cli"
	case InstallDeb:
		return "This binary was installed from a deb package. Update it with:\n    sudo apt-get update && sudo apt-get install --only-upgrade alpacon"
	case InstallRPM:
		return "This binary was installed from an rpm package. Update it with:\n    sudo yum update alpacon"
	case InstallVersionManager:
		return "This binary is managed by a version manager (mise or asdf). Update it through that tool."
	case InstallManual:
		return ""
	}
	return fmt.Sprintf("This binary is managed by its installer (%s). Update it through that tool.", kind) // A kind added without its wording. Saying nothing would read as "no guidance was needed", which is how a caller decides an update happened.
}
