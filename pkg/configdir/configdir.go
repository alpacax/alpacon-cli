package configdir

import (
	"fmt"
	"os"
	"path/filepath"
)

const Name = ".alpacon"

func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get user home directory: %w", err)
	}
	return filepath.Join(home, Name), nil
}
