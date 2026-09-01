//go:build windows

package cmd

const tempDirEnvVar = "TMP" // os.TempDir calls GetTempPath here, which reads TMP, TEMP and USERPROFILE and never TMPDIR.
