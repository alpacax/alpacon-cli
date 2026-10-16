package utils

import (
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	serverCodeShape = regexp.MustCompile(`^[a-z0-9_]+$`)
	httpCodeShape   = regexp.MustCompile(`^http_[0-9]+$`)
)

func loadServerErrorCodes(t *testing.T) []string {
	t.Helper()
	data, err := os.ReadFile("testdata/server_error_codes.txt")
	require.NoError(t, err)
	return strings.Split(strings.TrimSuffix(string(data), "\n"), "\n")
}

func TestServerErrorCodeListIsSortedUniqueAndWellFormed(t *testing.T) {
	t.Parallel()
	codes := loadServerErrorCodes(t)

	require.NotEmpty(t, codes)
	assert.True(t, slices.IsSorted(codes), "list must be sorted")
	assert.Len(t, slices.Compact(slices.Clone(codes)), len(codes), "list must have no duplicates")
	for _, code := range codes {
		assert.Regexp(t, serverCodeShape, code)
	}
}

func TestSyntheticErrorCodesDoNotCollideWithServerCodes(t *testing.T) {
	t.Parallel()
	codes := loadServerErrorCodes(t)

	for _, synthetic := range SyntheticErrorCodes() {
		assert.NotContains(t, codes, synthetic)
	}
	for _, code := range codes {
		assert.NotRegexp(t, httpCodeShape, code, "server code would collide with the http_<status> family")
	}
}
