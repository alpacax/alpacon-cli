//go:build darwin || linux

package utils

import (
	"bytes"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSaveFile_WriteFailure(t *testing.T) {
	for _, existing := range []bool{false, true} {
		name := "new file"
		if existing {
			name = "existing file"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			dest := filepath.Join(dir, "certificate.pem")
			if existing {
				require.NoError(t, os.WriteFile(dest, []byte("original"), 0600))
			}
			child := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestSaveFileWriteFailureHelperProcess$")
			child.Env = append(os.Environ(), "ALPACON_TEST_WRITE_FAILURE_PATH="+dest)
			output, err := child.CombinedOutput()
			require.NoError(t, err, "%s", output)

			if existing {
				content, err := os.ReadFile(dest)
				require.NoError(t, err)
				assert.Equal(t, "original", string(content))
			} else {
				_, err := os.Stat(dest)
				require.ErrorIs(t, err, os.ErrNotExist)
			}
			staged, err := filepath.Glob(filepath.Join(dir, ".alpacon-*.tmp"))
			require.NoError(t, err)
			assert.Empty(t, staged)
		})
	}
}

func TestSaveFileWriteFailureHelperProcess(t *testing.T) {
	dest := os.Getenv("ALPACON_TEST_WRITE_FAILURE_PATH")
	if dest == "" {
		return
	}
	signal.Ignore(syscall.SIGXFSZ)
	var limit syscall.Rlimit
	require.NoError(t, syscall.Getrlimit(syscall.RLIMIT_FSIZE, &limit))
	limit.Cur = 1024
	require.NoError(t, syscall.Setrlimit(syscall.RLIMIT_FSIZE, &limit))
	require.ErrorIs(t, SaveFile(dest, bytes.Repeat([]byte("x"), 2048)), syscall.EFBIG)
}
