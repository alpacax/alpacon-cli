package server

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestValidPlatforms_ContainsWindows(t *testing.T) {
	assert.Contains(t, validPlatforms, "windows")
}

func TestValidPlatforms_ContainsAll(t *testing.T) {
	expected := []string{"debian", "rhel", "darwin", "windows"}
	for _, p := range expected {
		assert.Contains(t, validPlatforms, p, "validPlatforms should contain %q", p)
	}
}

func TestValidPlatformsList_IncludesWindows(t *testing.T) {
	assert.Contains(t, validPlatformsList, "windows")
}
