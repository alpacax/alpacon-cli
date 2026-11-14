//go:build !windows

package config

import "os"

func renameConfig(from, to string) error { return os.Rename(from, to) }
