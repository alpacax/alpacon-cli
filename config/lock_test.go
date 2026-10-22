package config

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/alpacax/alpacon-cli/pkg/filelock"
)

const (
	configWriterEnv    = "ALPACON_CONFIG_WRITER_CHILD"
	configWriterRounds = 150
)

// runConfigWriter is one child process's share of the race.
func runConfigWriter(name string) error {
	for i := range configWriterRounds {
		if err := SetActiveWorkSessionFor(fmt.Sprintf("%s-%d", name, i), "uuid"); err != nil {
			return err
		}
		if name != "b" {
			continue
		}
		if err := SaveRefreshedAuth0Token("refresh", "token-"+strconv.Itoa(i), 3600); err != nil {
			return err
		}
	}
	return nil
}

func TestConcurrentProcessesKeepEachOthersConfigChanges(t *testing.T) {
	if name := os.Getenv(configWriterEnv); name != "" {
		if err := runConfigWriter(name); err != nil {
			_, _ = fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	// Given a config and two alpacon processes that each change their own fields
	setupTestConfig(t)
	require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "token-start", "refresh", "alpacon.io", 3600, false))
	children := make([]*exec.Cmd, 0, 2)
	for _, name := range []string{"a", "b"} {
		child := exec.Command(os.Args[0], "-test.run=^TestConcurrentProcessesKeepEachOthersConfigChanges$")
		child.Env = append(os.Environ(), configWriterEnv+"="+name)
		children = append(children, child)
	}

	// When both run at once
	outputs := make([][]byte, len(children))
	errs := make([]error, len(children))
	done := make(chan int, len(children))
	for i, child := range children {
		go func() {
			outputs[i], errs[i] = child.CombinedOutput()
			done <- i
		}()
	}
	for range children {
		<-done
	}

	// Then every change either of them made is still in the file
	for i := range children {
		require.NoError(t, errs[i], "writer output: %s", outputs[i])
	}
	cfg, err := LoadConfig()
	require.NoError(t, err)
	var lost []string
	for _, name := range []string{"a", "b"} {
		for i := range configWriterRounds {
			if key := fmt.Sprintf("%s-%d", name, i); cfg.ActiveWorkSessions[key] == "" {
				lost = append(lost, key)
			}
		}
	}
	assert.Empty(t, lost)
	assert.Equal(t, "token-"+strconv.Itoa(configWriterRounds-1), cfg.AccessToken)
}

func configLockPath(t *testing.T) string {
	t.Helper()
	dir, err := configDir()
	require.NoError(t, err)
	return filepath.Join(dir, lockFileName)
}

func holdConfigLock(t *testing.T) {
	t.Helper()
	held, err := filelock.Acquire(configLockPath(t), 0644)
	require.NoError(t, err)
	t.Cleanup(func() { _ = held.Release() })
}

func TestWithConfigLockGivesUpAfterTheTimeoutWithoutRunning(t *testing.T) {
	setupTestConfig(t)
	holdConfigLock(t)

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ran := false

		err := withConfigLock(func() error { ran = true; return nil })

		require.ErrorContains(t, err, fmt.Sprintf("timed out after %s waiting for another alpacon process", lockTimeout))
		assert.False(t, ran)
		assert.GreaterOrEqual(t, time.Since(start), lockTimeout)
		assert.Less(t, time.Since(start), lockTimeout+lockRetryInterval)
	})
}

func TestWithConfigLockFailsAtOnceWhenTheLockFileCannotBeOpened(t *testing.T) {
	setupTestConfig(t)
	require.NoError(t, os.WriteFile(filepath.Join(os.Getenv("HOME"), ConfigFileDir), nil, 0600)) // a file where the directory belongs

	synctest.Test(t, func(t *testing.T) {
		start := time.Now()
		ran := false

		err := withConfigLock(func() error { ran = true; return nil })

		require.ErrorContains(t, err, "failed to lock")
		assert.NotContains(t, err.Error(), "timed out")
		assert.False(t, ran)
		assert.Zero(t, time.Since(start))
	})
}

func TestWithConfigLockHoldsTheLockWhileFnRunsAndReleasesItAfter(t *testing.T) {
	tests := []struct {
		name    string
		fnErr   error
		wantErr error
	}{
		{name: "fn succeeds"},
		{name: "fn fails", fnErr: assert.AnError, wantErr: assert.AnError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			lockPath := configLockPath(t)
			var heldDuringFn error

			err := withConfigLock(func() error {
				_, heldDuringFn = filelock.Acquire(lockPath, 0644)
				return tt.fnErr
			})

			assert.Equal(t, tt.wantErr, err)
			require.ErrorIs(t, heldDuringFn, filelock.ErrLocked)
			lock, err := filelock.Acquire(lockPath, 0644)
			require.NoError(t, err)
			assert.NoError(t, lock.Release())
		})
	}
}

func TestEveryWriterWaitsForTheLockAndLeavesTheFileAlone(t *testing.T) {
	writers := []struct {
		name  string
		write func(started Config) error
	}{
		{name: "login", write: func(Config) error {
			return CreateConfig("https://ws2.us1.alpacon.io", "ws2", "", "", "access-2", "refresh-2", "alpacon.io", 3600, false)
		}},
		{name: "logout", write: DeleteConfig},
		{name: "switch", write: func(started Config) error {
			return SwitchWorkspace(started, "https://ws2.us1.alpacon.io", "ws2", false)
		}},
		{name: "revert", write: func(started Config) error { return RestoreWorkspace(started.WorkspaceURL, started) }},
		{name: "workspace env", write: func(started Config) error { return SetWorkspaceEnv(started.WorkspaceURL, "frozen", true) }},
		{name: "token refresh", write: func(started Config) error {
			return SaveRefreshedAuth0Token(started.RefreshToken, "access-2", 3600)
		}},
		{name: "work session", write: func(Config) error { return SetActiveWorkSession("uuid-2") }},
		{name: "work session for a workspace", write: func(Config) error { return SetActiveWorkSessionFor("ws1", "uuid-2") }},
		{name: "work session clear", write: func(Config) error {
			_, err := ClearActiveWorkSessionFor("ws1", "uuid-1")
			return err
		}},
	}
	for _, tt := range writers {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "", "", "access-1", "refresh-1", "alpacon.io", 3600, false))
			require.NoError(t, SetActiveWorkSession("uuid-1"))
			started := loadedConfig(t)
			configFile := filepath.Join(os.Getenv("HOME"), ConfigFileDir, ConfigFileName)
			before, err := os.ReadFile(configFile)
			require.NoError(t, err)
			holdConfigLock(t)

			synctest.Test(t, func(t *testing.T) {
				err := tt.write(started)

				require.ErrorContains(t, err, "timed out")
			})
			after, err := os.ReadFile(configFile)
			require.NoError(t, err)
			assert.Equal(t, string(before), string(after))
		})
	}
}

func TestUpdateConfigLeavesTheFileAloneWhenApplyChangesNothing(t *testing.T) {
	tests := []struct {
		name     string
		applyErr error
	}{
		{name: "apply reports no change"},
		{name: "apply fails", applyErr: assert.AnError},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setupTestConfig(t)
			require.NoError(t, CreateConfig("https://ws1.us1.alpacon.io", "ws1", "token", "", "", "", "", 0, false))
			configFile := filepath.Join(os.Getenv("HOME"), ConfigFileDir, ConfigFileName)
			before, err := os.Stat(configFile)
			require.NoError(t, err)

			err = updateConfig(func(cfg *Config) (bool, error) {
				cfg.Token = "changed"
				return false, tt.applyErr
			})

			assert.Equal(t, tt.applyErr, err)
			after, err := os.Stat(configFile)
			require.NoError(t, err)
			assert.True(t, os.SameFile(before, after), "a save renames a new file over the config")
			assert.Equal(t, "token", loadedConfig(t).Token)
		})
	}
}

// saveConfig itself takes no lock, so every use of it must sit inside a function
// literal handed straight to withConfigLock.
func TestSaveConfigIsOnlyUsedInsideWithConfigLock(t *testing.T) {
	t.Parallel()
	entries, err := os.ReadDir(".")
	require.NoError(t, err)

	uses := 0
	var outside []string
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			continue
		}
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, 0)
		require.NoError(t, err)
		var stack []ast.Node
		ast.Inspect(file, func(n ast.Node) bool {
			if n == nil {
				stack = stack[:len(stack)-1]
				return true
			}
			if ident, ok := n.(*ast.Ident); ok && ident.Name == "saveConfig" && !declaresSaveConfig(stack) {
				uses++
				if !insideWithConfigLock(stack) {
					outside = append(outside, fset.Position(ident.Pos()).String())
				}
			}
			stack = append(stack, n)
			return true
		})
	}

	require.NotZero(t, uses, "the walk found no use of saveConfig, so it checked nothing")
	assert.Empty(t, outside)
}

func declaresSaveConfig(stack []ast.Node) bool {
	fn, ok := stack[len(stack)-1].(*ast.FuncDecl)
	return ok && fn.Name.Name == "saveConfig"
}

func insideWithConfigLock(stack []ast.Node) bool {
	for i := len(stack) - 1; i > 0; i-- {
		lit, ok := stack[i].(*ast.FuncLit)
		if !ok {
			continue
		}
		call, ok := stack[i-1].(*ast.CallExpr)
		if !ok {
			continue
		}
		if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "withConfigLock" && slices.Contains(call.Args, ast.Expr(lit)) {
			return true
		}
	}
	return false
}
