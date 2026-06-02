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

// inlineCommandLimit matches alpacon-server Command.line max_length=2048.
const inlineCommandLimit = 2048

const tempScriptDir = "/tmp"

// exceedsInlineLimit is byte-based: may bypass before the server's char limit, never after.
func exceedsInlineLimit(command string) bool {
	return len(command) > inlineCommandLimit
}

func tempScriptName(id string) string {
	return fmt.Sprintf(".alpacon-exec-%s.sh", id)
}

func tempScriptPath(id string) string {
	return tempScriptDir + "/" + tempScriptName(id)
}

func wrapScriptCommand(path string) string {
	return fmt.Sprintf("sh %s; rc=$?; rm -f %s; exit $rc", path, path)
}

func isWindowsPlatform(platform string) bool {
	return strings.EqualFold(strings.TrimSpace(platform), "windows")
}

func newExecID() string {
	b := make([]byte, 8)
	_, _ = rand.Read(b) // failure is effectively impossible; all-zero b stays valid hex
	return hex.EncodeToString(b)
}

// runOversizedCommand uploads an over-limit command as a temp script and runs it with sh.
// Caller must have confirmed OAuth auth; Windows is rejected before upload.
func runOversizedCommand(ac *client.AlpaconClient, parsed RemoteExecArgs, env map[string]string, workSessionID, authMethod string) {
	// Shared MFA / username / token-refresh retry, parameterized by the operation, so
	// platform resolution and upload retry like the inline path on transient auth errors.
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
			// WorkSession gate denial here must surface as exit 3, like the paths below.
			utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
			utils.CliErrorWithExit("failed to resolve server platform for '%s': %s", parsed.Server, err)
			return
		}
	}
	if isWindowsPlatform(platform) {
		utils.CliErrorWithExit("Large commands (>2KB) are not supported on Windows servers.\n"+
			"The upload bypass relies on POSIX sh/tmp/rm semantics. Shorten the command, "+
			"or run it through a Windows-native mechanism.")
		return
	}

	id := newExecID()
	scriptPath := tempScriptPath(id)

	utils.CliInfo("Command exceeds 2KB; uploading via temporary file...")

	// allowOverwrite=false: scriptPath is fresh-random, so a hit means collision/leftover—don't clobber.
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

	// rm -f in the wrapper cleans up; if submission fails first, a unique /tmp dotfile is left for normal tmp cleanup.
	wrapper := wrapScriptCommand(scriptPath)

	if parsed.Detach {
		runDetached(ac, parsed, wrapper, env, workSessionID, authMethod)
		return
	}

	result, err := RunCommandWithRetry(ac, parsed.Server, wrapper, parsed.Username, parsed.Groupname, env, workSessionID)
	utils.HandleWorkSessionError(err, "command", parsed.Server, authMethod, workSessionID)
	HandleCommandResult(result, err)
}
