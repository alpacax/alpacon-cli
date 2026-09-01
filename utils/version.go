package utils

const DevVersion = "dev" // What Version carries when no -ldflags set it.

var Version string = DevVersion

func GetCLIVersion() string {
	return Version
}
