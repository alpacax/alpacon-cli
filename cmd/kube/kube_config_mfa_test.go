package kube

import (
	"errors"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/alpacax/alpacon-cli/pkg/kubeconfig"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A workspace that requires MFA for kube exec refuses the first open with
// 401 auth_mfa_required. The command asks for the step-up once, through the
// workspace's link, and opens the session when the retry passes.
// Serial: t.Setenv keeps the step-up from opening a browser.
func TestOpenKubeConfigStepsUpWhenTheOpenNeedsMFA(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	var lockedDuringWait atomic.Bool
	srv.onCreate = func(call int) {
		if call == 2 {
			_, err := os.Stat(path + ".lock")
			lockedDuringWait.Store(err == nil)
		}
	}
	ac := srv.startInMemory(t)

	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		result, err := openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
		require.NoError(t, err)
		assert.Equal(t, testSessionID, result.KubeSessionID)
		// Taken again before the write, and held until the command lets go.
		assert.FileExists(t, path+".lock")
		hold.close()
	})

	assert.Equal(t, 2, srv.creates())
	assert.Equal(t, 0, srv.closes())
	assert.False(t, lockedDuringWait.Load(), "kubectl must not be locked out of the kubeconfig during the MFA wait")
	assert.NoFileExists(t, path+".lock")
	assert.Equal(t, testToken, readKubeconfig(t, path).Users[0].User["token"])

	links := srv.mfaLinkQueries()
	require.Len(t, links, 1)
	assert.Equal(t, "cli", links[0].Get("location"))
	assert.Equal(t, "my-workspace", links[0].Get("workspace"))
	// A kube session has a cluster, not a server, so the link names none.
	assert.False(t, links[0].Has("server"), "link query: %v", links[0])
}

// kube login saves the session the retry opened, not the refused attempt.
// Serial: t.Setenv.
func TestOpenKubeSessionSavesTheSessionOpenedAfterMFA(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	ac := srv.startInMemory(t)
	var saved []string
	writer := kubeSessionWriter{
		exec: credentialPlugin("prod-east"),
		persist: func(session *kubernetes.KubeSessionResponse) (func() error, error) {
			saved = append(saved, session.ID)
			return func() error { return nil }, nil
		},
	}

	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err := openKubeSession(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path}, "c-prod", writer)
		require.NoError(t, err)
	})

	assert.Equal(t, []string{testSessionID}, saved)
	assert.Equal(t, 2, srv.creates())
	cfg := readKubeconfig(t, path)
	require.Len(t, cfg.Users, 1)
	assert.NotContains(t, cfg.Users[0].User, "token")
}

// A first Ctrl+C during the wait ends it at the next attempt: no session is
// opened only to be closed, and the kubeconfig is left as it was.
// Serial: t.Setenv.
func TestOpenKubeConfigStopsTheMFAWaitAtAnInterrupt(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	var interrupted atomic.Bool
	srv := newSessionServer()
	srv.mfaCreates = 1
	srv.onCreate = func(int) { interrupted.Store(true) }
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{
			clusterName: "prod-east",
			kubeconfig:  path,
			interrupted: interrupted.Load,
		})
	})

	require.ErrorIs(t, err, errInterrupted)
	assert.Equal(t, 1, srv.creates())
	assert.Equal(t, 0, srv.closes())
	// Interrupted during the refused create: no link is fetched and no
	// browser opened for a wait that would end at once.
	assert.Empty(t, srv.mfaLinkQueries())
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock")
	exitErr := kubeConfigError(err, "prod-east", "")
	assert.Contains(t, exitErr.Error(), "The kubeconfig was not written: interrupted")
}

// After the step-up the server judges the WorkSession, and a gate refusal then
// ends the command as one does without MFA.
// Serial: t.Setenv.
func TestOpenKubeConfigReportsAGateRefusalAfterMFA(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	srv.createStatus = 403
	srv.createCode = utils.WorkSessionExpired
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", workSessionID: testSessionID, kubeconfig: path})
	})

	require.Error(t, err)
	assert.Equal(t, 2, srv.creates())
	var exitErr *utils.ExitError
	require.ErrorAs(t, kubeConfigError(err, "prod-east", testSessionID), &exitErr)
	assert.Equal(t, utils.ExitCodeWorkSessionDenied, exitErr.Code)
	assert.NoFileExists(t, path)
}

// If another program holds the kubeconfig lock past the wait for it, the
// opened session is closed, since its token could not be written.
// Serial: t.Setenv.
func TestOpenKubeConfigClosesTheSessionWhenTheLockCannotBeRetaken(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	srv.onCreate = func(call int) {
		if call == 2 {
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Errorf("plant a foreign lock: %v", err)
			}
		}
	}
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path, relockTimeout: time.Second})
	})

	require.Error(t, err)
	require.ErrorIs(t, err, kubeconfig.ErrLocked)
	assert.Contains(t, err.Error(), "the kubeconfig could not be opened again")
	assert.Contains(t, err.Error(), "the kube session was closed")
	assert.Equal(t, 1, srv.closes())
	// The other program's lock is its own to remove.
	assert.FileExists(t, path+".lock")
}

// kubectl holds the lock only while it writes, so a lock that clears within
// the wait for it costs the session nothing.
// Serial: t.Setenv.
func TestOpenKubeConfigWaitsOutABriefKubeconfigLock(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	srv.onCreate = func(call int) {
		if call == 2 {
			if err := os.WriteFile(path+".lock", nil, 0o600); err != nil {
				t.Errorf("plant a foreign lock: %v", err)
			}
			go func() {
				time.Sleep(300 * time.Millisecond)
				_ = os.Remove(path + ".lock")
			}()
		}
	}
	ac := srv.startInMemory(t)

	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err := openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
		require.NoError(t, err)
	})

	assert.Equal(t, 0, srv.closes())
	assert.Equal(t, testToken, readKubeconfig(t, path).Users[0].User["token"])
}

// A step-up link the server will not give ends the command with the reason,
// and the lock it let go stays released.
// Serial: t.Setenv.
func TestOpenKubeConfigReportsAFailedMFALink(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	// kubeSessionError reads the login's auth method from HOME.
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	srv.mfaLinkStatus = 400
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
	})

	require.Error(t, err)
	assert.Equal(t, 1, srv.creates())
	assert.NoFileExists(t, path+".lock")
	var exitErr *utils.ExitError
	require.ErrorAs(t, kubeSessionError(err, "prod-east", "", opKubeLogin), &exitErr)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	assert.Contains(t, exitErr.Error(), "Failed to open a kube session: MFA authentication failed")
}

// MFA never finished: the wait ends at its timeout with nothing opened, and
// the command says why.
// Serial: t.Setenv.
func TestOpenKubeConfigEndsTheMFAWaitAtItsTimeout(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	// kubeSessionError reads the login's auth method from HOME.
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1 << 30
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
	})

	require.Error(t, err)
	assert.Greater(t, srv.creates(), 2)
	assert.Equal(t, 0, srv.closes())
	assert.Len(t, srv.mfaLinkQueries(), 1, "one prompt for the whole wait")
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock")
	var exitErr *utils.ExitError
	require.ErrorAs(t, kubeConfigError(err, "prod-east", ""), &exitErr)
	assert.Equal(t, utils.ExitCodeGeneralError, exitErr.Code)
	assert.Contains(t, exitErr.Error(), "Failed to open a kube session: MFA authentication timed out")
}

// A refusal other than MFA keeps the lock and asks for no step-up.
func TestOpenKubeConfigAsksNoMFAForOtherRefusals(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.createStatus = 409
	srv.createCode = utils.ClusterNotConnected
	ac := srv.start(t)
	hold := openHold(t, path)

	_, err := openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	require.Error(t, err)
	assert.Equal(t, 1, srv.creates())
	assert.Empty(t, srv.mfaLinkQueries())
	assert.FileExists(t, path+".lock")
	assert.False(t, utils.IsSubmissionError(errors.Unwrap(err)), "the tag must not leave the command: %v", err)
}

// A second Ctrl+C closes the hold from the watcher while the command may still
// be on its way to the write. The command then closes the session it opened
// instead of reaching for an editor that is gone.
func TestOpenKubeConfigClosesTheSessionWhenTheHoldClosesUnderIt(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config")
	hold := openHold(t, path)
	srv := newSessionServer()
	srv.onCreate = func(int) { hold.close() }
	ac := srv.start(t)

	_, err := openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})

	require.ErrorIs(t, err, errInterrupted)
	assert.Equal(t, 1, srv.closes())
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock")
}

// A second Ctrl+C during the MFA wait closes the hold from the watcher. When
// the retry has already opened the session, the command closes it instead of
// taking the lock again or writing.
// Serial: t.Setenv.
func TestOpenKubeConfigClosesTheSessionWhenTheHoldClosesDuringTheMFAWait(t *testing.T) {
	t.Setenv("ALPACON_NO_BROWSER", "1")
	path := filepath.Join(t.TempDir(), "config")
	srv := newSessionServer()
	srv.mfaCreates = 1
	ac := srv.startInMemory(t)

	var err error
	synctest.Test(t, func(t *testing.T) {
		hold := openHold(t, path)
		srv.onCreate = func(call int) {
			if call == 2 {
				hold.close()
			}
		}
		_, err = openKubeConfig(ac, hold, kubeConfigOptions{clusterName: "prod-east", kubeconfig: path})
	})

	require.ErrorIs(t, err, errInterrupted)
	assert.Equal(t, 2, srv.creates())
	assert.Equal(t, 1, srv.closes())
	assert.NoFileExists(t, path)
	assert.NoFileExists(t, path+".lock")
}
