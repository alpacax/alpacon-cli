package exec

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// inlineCommandLimit is the max command size (bytes) sent inline; larger commands use the upload bypass. Matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

// tempScriptDir is the remote directory where oversized command scripts are
// staged before execution.
const tempScriptDir = "/tmp"

// exceedsInlineLimit reports whether command is too large to send inline. Byte-based, so it may bypass slightly before the server's char limit but never after—the safe direction.
func exceedsInlineLimit(command string) bool {
	return len(command) > inlineCommandLimit
}

// tempScriptName returns the remote filename for an oversized command script.
func tempScriptName(id string) string {
	return fmt.Sprintf(".alpacon-exec-%s.sh", id)
}

// tempScriptPath returns the absolute remote path for an oversized command script.
func tempScriptPath(id string) string {
	return tempScriptDir + "/" + tempScriptName(id)
}

// wrapScriptCommand builds the inline wrapper that runs the uploaded script,
// preserves its exit code, and removes the temp file even on non-zero exit.
func wrapScriptCommand(path string) string {
	return fmt.Sprintf("sh %s; rc=$?; rm -f %s; exit $rc", path, path)
}

// isWindowsPlatform reports whether the resolved platform is Windows; empty/unknown is treated as POSIX and proceeds.
func isWindowsPlatform(platform string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "windows")
}

// newExecID returns a short random hex identifier for a temp script path,
// avoiding collisions between concurrent oversized executions.
func newExecID() string {
	b := make([]byte, 8)
	// rand.Read failing is effectively impossible; all-zero b stays hex on failure.
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// runOversizedCommand uploads an over-limit command as a temp script and runs it with sh. Caller must have confirmed OAuth auth; Windows is rejected before upload. With --detach only the wrapper is submitted detached.
func runOversizedCommand(ac *client.AlpaconClient, parsed RemoteExecArgs, env map[string]string, workSessionID, authMethod string) {
	// Shared MFA / username-required / token-refresh handling parameterized by the operation to retry, so platform resolution and upload retry like the inline path instead of hard-failing on a transient auth error.
	commonCallbacks := func(retry func() error) utils.ErrorHandlerCallbacks {
		return utils.ErrorHandlerCallbacks{
			OnMFARequired: func(srv string) error {
				return mfa.HandleMFAError(ac, srv)
			},
			OnUsernameRequired: func() error {
				_, e := iam.HandleUsernameRequired()
				return e
			},
			CheckMFACompleted: func() (bool, error) {
				return mfa.CheckMFACompletion(ac)
			},
			RefreshToken:   ac.RefreshToken,
			RetryOperation: retry,
		}
	}

	var platform string
	resolvePlatform := func() error {
		p, e := server.GetServerPlatform(ac, parsed.Server)
		if e != nil {
			return e
		}
		platform = p
		return nil
	}
	if err := resolvePlatform(); err != nil {
		if err = utils.HandleCommonErrors(err, parsed.Server, commonCallbacks(resolvePlatform)); err != nil {
			// A WorkSession gate denial on the server-detail read must surface as
			// exit 3, consistent with the upload and command paths below.
			utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
			utils.CliErrorWithExit("failed to resolve server platform for '%s': %s", parsed.Server, err)
			return
		}
	}
	if isWindowsPlatform(platform) {
		utils.CliErrorWithExit("Large commands (>2KB) are not supported on Windows servers.\n" +
			"The upload bypass relies on POSIX sh/tmp/rm semantics. Shorten the command, " +
			"or run it through a Windows-native mechanism.")
		return
	}

	id := newExecID()
	scriptPath := tempScriptPath(id)

	utils.CliInfo("Command exceeds 2KB; uploading via temporary file...")

	// allowOverwrite is false: scriptPath is a fresh random path, so an existing
	// file means an unexpected collision or leftover—surface it, don't clobber.
	upload := func() error {
		return ftp.UploadContent(ac, parsed.Server, scriptPath,
			[]byte(parsed.Command), parsed.Username, parsed.Groupname, false, workSessionID)
	}

	if err := upload(); err != nil {
		if err = utils.HandleCommonErrors(err, parsed.Server, commonCallbacks(upload)); err != nil {
			utils.HandleWorkSessionError(err, "webftp", parsed.Server, authMethod, workSessionID)
			utils.CliErrorWithExit("failed to upload command to '%s': %s", parsed.Server, err)
			return
		}
	}

	// The wrapper's rm -f cleans up the script. If submission fails before the agent runs it, a unique dotfile is left under /tmp, reaped by normal tmp cleanup.
	wrapper := wrapScriptCommand(scriptPath)

	if parsed.Detach {
		runDetached(ac, parsed, wrapper, env, workSessionID, authMethod)
		return
	}

	result, err := RunCommandWithRetry(ac, parsed.Server, wrapper, parsed.Username, parsed.Groupname, env, workSessionID)
	utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
	HandleCommandResult(result, err)
}
