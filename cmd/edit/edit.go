package edit

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	ftpapi "github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/cmd/worksession"
	"github.com/alpacax/alpacon-cli/config"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

const maxEditPromptSize int64 = 10 * 1024 * 1024

// guiEditors are editors that, without a wait flag, spawn a window and return
// immediately—so edit would hash an unchanged file and skip the upload.
var guiEditors = map[string]bool{
	"code":          true,
	"code-insiders": true,
	"codium":        true,
	"vscodium":      true,
	"cursor":        true,
	"windsurf":      true,
	"subl":          true,
	"sublime_text":  true,
	"atom":          true,
	"mate":          true,
	"zed":           true,
}

var EditCmd = &cobra.Command{
	Use:   "edit [USER@]SERVER:PATH",
	Short: "Edit a remote file with your local editor",
	Long: `Download a single remote file, open it in your local editor, and upload
changes back to the original path when the editor exits.

The command uses the same WebFTP transport and permission behavior as 'alpacon cp'.
Use -u/--username and -g/--groupname the same way you would with 'alpacon cp'.
Interactive browser login requires an active WorkSession with the webftp scope.

The file is downloaded in full before editing; files larger than 10 MB prompt
for confirmation (skipped with --force) before opening in the editor.`,
	Example: `  alpacon edit my-server:/etc/nginx/nginx.conf
  alpacon edit my-server:/etc/nginx/nginx.conf --editor "code --wait"
  alpacon edit my-server:/var/log/large.txt --force`,
	Args: cobra.ExactArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		editorFlag, _ := cmd.Flags().GetString("editor")
		usernameFlag, _ := cmd.Flags().GetString("username")
		groupname, _ := cmd.Flags().GetString("groupname")
		force, _ := cmd.Flags().GetBool("force")
		flagWorkSession, _ := cmd.Flags().GetString("work-session")

		target, err := parseEditTarget(args[0], usernameFlag)
		if err != nil {
			utils.CliErrorWithExit("%s", err)
		}

		workSessionID := worksession.ResolveAndAnnounce(flagWorkSession)
		authMethod := config.ResolveAuthMethod()

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s. Consider re-logging.", err)
		}

		deps := realEditDeps(alpaconClient, groupname)
		result, err := runEdit(editOptions{
			Target:        target,
			Editor:        editorFlag,
			Force:         force,
			WorkSessionID: workSessionID,
		}, deps)
		if err != nil {
			printPreservedTempPath(result)
			utils.HandleWorkSessionError(err, "webftp", target.Server, authMethod, workSessionID)
			utils.CliErrorWithExit("Failed to edit '%s:%s': %s", target.Server, target.RemotePath, err)
		}

		if !result.Changed {
			utils.CliInfo("No changes")
			return
		}
		utils.CliSuccess("Uploaded changes to %s:%s", target.Server, target.RemotePath)
	},
}

type editTarget struct {
	Server     string
	RemotePath string
	Username   string
}

type editOptions struct {
	Target        editTarget
	Editor        string
	Force         bool
	WorkSessionID string
}

type editResult struct {
	TempPath string
	Changed  bool
}

type editDeps struct {
	download     func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error)
	upload       func(target editTarget, localPath, workSessionID string) error
	runEditor    func(editor, filePath string) error
	confirmLarge func(size int64) bool
	removeAll    func(path string) error
	tempRoot     string
}

func init() {
	EditCmd.Flags().String("editor", "", "Editor command to run (default: ALPACON_EDITOR, VISUAL, EDITOR, then vi)")
	EditCmd.Flags().Bool("force", false, "Edit files larger than 10 MB without prompting")
	EditCmd.Flags().StringP("username", "u", "", "Specify username")
	EditCmd.Flags().StringP("groupname", "g", "", "Specify groupname")
	EditCmd.Flags().String("work-session", "", "Attach this edit to a work-session (overrides 'work-session use')")
}

func realEditDeps(ac *client.AlpaconClient, groupname string) editDeps {
	return editDeps{
		download: func(target editTarget, localPath, workSessionID string) (ftpapi.DownloadedFile, error) {
			downloaded, err := ftpapi.DownloadFileToPath(ac, target.Server, target.RemotePath, localPath, target.Username, groupname, workSessionID)
			if err != nil {
				err = utils.HandleCommonErrors(err, target.Server, editErrorCallbacks(ac, func() error {
					var retryErr error
					downloaded, retryErr = ftpapi.DownloadFileToPath(ac, target.Server, target.RemotePath, localPath, target.Username, groupname, workSessionID)
					return retryErr
				}))
			}
			return downloaded, err
		},
		upload: func(target editTarget, localPath, workSessionID string) error {
			err := ftpapi.UploadLocalFileAs(ac, localPath, target.Server, target.RemotePath, target.Username, groupname, workSessionID)
			if err != nil {
				err = utils.HandleCommonErrors(err, target.Server, editErrorCallbacks(ac, func() error {
					return ftpapi.UploadLocalFileAs(ac, localPath, target.Server, target.RemotePath, target.Username, groupname, workSessionID)
				}))
			}
			return err
		},
		runEditor:    runLocalEditor,
		confirmLarge: confirmLargeEdit,
	}
}

func editErrorCallbacks(ac *client.AlpaconClient, retry func() error) utils.ErrorHandlerCallbacks {
	return utils.ErrorHandlerCallbacks{
		OnMFARequired: func(serverName string) error {
			return mfa.HandleMFAError(ac, serverName)
		},
		OnUsernameRequired: func() error {
			_, err := iam.HandleUsernameRequired()
			return err
		},
		CheckMFACompleted: func() (bool, error) {
			return mfa.CheckMFACompletion(ac)
		},
		RefreshToken:   ac.RefreshToken,
		RetryOperation: retry,
	}
}

func parseEditTarget(arg, usernameFlag string) (editTarget, error) {
	if !utils.IsRemoteTarget(arg) {
		return editTarget{}, fmt.Errorf("remote path must be in format [USER@]SERVER:PATH")
	}
	sshTarget := utils.ParseSSHTarget(arg)
	if sshTarget.Host == "" || sshTarget.Path == "" {
		return editTarget{}, fmt.Errorf("remote path must include both server and path")
	}
	username := usernameFlag
	if username == "" {
		username = sshTarget.User
	}
	return editTarget{Server: sshTarget.Host, RemotePath: sshTarget.Path, Username: username}, nil
}

func resolveEditor(flagValue string) string {
	if strings.TrimSpace(flagValue) != "" {
		return strings.TrimSpace(flagValue)
	}
	if alpaconEditor := strings.TrimSpace(os.Getenv("ALPACON_EDITOR")); alpaconEditor != "" {
		return alpaconEditor
	}
	if visual := strings.TrimSpace(os.Getenv("VISUAL")); visual != "" {
		return visual
	}
	if editor := strings.TrimSpace(os.Getenv("EDITOR")); editor != "" {
		return editor
	}
	return "vi"
}

// guiEditorWaitWarning returns a warning when the resolved editor is a known GUI
// editor invoked without a wait flag. Such editors return before the user saves,
// so the before/after hash is unchanged and edit reports "No changes" and skips
// the upload—the classic git core.editor footgun.
func guiEditorWaitWarning(editor string) string {
	parts, err := splitEditorCommand(editor)
	if err != nil || len(parts) == 0 {
		return ""
	}
	name := strings.ToLower(filepath.Base(parts[0]))
	name = strings.TrimSuffix(name, ".exe")
	if !guiEditors[name] {
		return ""
	}
	for _, arg := range parts[1:] {
		if arg == "-w" || arg == "--wait" {
			return ""
		}
	}
	return fmt.Sprintf("'%s' looks like a GUI editor launched without a wait flag; it may return before you save, so changes would not be uploaded. Add a wait flag, e.g. --editor \"%s --wait\"", name, name)
}

func runEdit(opts editOptions, deps editDeps) (editResult, error) {
	deps = normalizeEditDeps(deps)
	tempPath, err := editTempPath(deps.tempRoot, opts.Target)
	result := editResult{TempPath: tempPath}
	if err != nil {
		return result, err
	}

	downloaded, err := deps.download(opts.Target, tempPath, opts.WorkSessionID)
	if err != nil {
		cleanupEditTemp(tempPath, deps.removeAll)
		return result, err
	}
	if downloaded.Path != "" {
		result.TempPath = downloaded.Path
	}

	// Restrict the downloaded file to the owner; edit handles sensitive remote files.
	// The containing directories are already 0700, so this is defense-in-depth.
	// Note: editors that save by writing a new inode and renaming over the file
	// reset the mode to the process umask, but the 0700 parent still gates access.
	if err := os.Chmod(result.TempPath, 0600); err != nil {
		cleanupEditTemp(result.TempPath, deps.removeAll)
		return result, fmt.Errorf("failed to secure temp file: %w", err)
	}

	// The size guard runs post-download: it uses the actual bytes written to
	// disk, which are only known after the fetch, so the guard prevents opening
	// an oversized file in the editor rather than saving bandwidth.
	if downloaded.Size > maxEditPromptSize && !opts.Force && !deps.confirmLarge(downloaded.Size) {
		cleanupEditTemp(result.TempPath, deps.removeAll)
		return result, fmt.Errorf("remote file is larger than 10 MB; rerun with --force to edit it")
	}

	before, err := hashFile(result.TempPath)
	if err != nil {
		cleanupEditTemp(result.TempPath, deps.removeAll)
		return result, err
	}

	editor := resolveEditor(opts.Editor)
	if warning := guiEditorWaitWarning(editor); warning != "" {
		utils.CliWarning("%s", warning)
	}
	editorErr := deps.runEditor(editor, result.TempPath)

	after, err := hashFile(result.TempPath)
	if err != nil {
		if editorErr != nil {
			return result, fmt.Errorf("editor failed: %w", editorErr)
		}
		return result, err
	}
	result.Changed = before != after

	if editorErr != nil {
		// The editor failed: preserve the temp only if the user actually changed
		// it, otherwise it is just an unmodified copy of the remote file.
		if !result.Changed {
			cleanupEditTemp(result.TempPath, deps.removeAll)
		}
		return result, fmt.Errorf("editor failed: %w", editorErr)
	}

	if !result.Changed {
		cleanupEditTemp(result.TempPath, deps.removeAll)
		return result, nil
	}

	if err := deps.upload(opts.Target, result.TempPath, opts.WorkSessionID); err != nil {
		return result, err
	}

	cleanupEditTemp(result.TempPath, deps.removeAll)
	return result, nil
}

func cleanupEditTemp(filePath string, removeAll func(string) error) {
	if filePath == "" {
		return
	}
	sessionDir := filepath.Dir(filePath)
	if err := removeAll(sessionDir); err != nil {
		// Surface failures: the temp may hold a copy of a sensitive remote file.
		utils.CliWarning("Failed to remove temp directory %s: %s", sessionDir, err)
		return
	}
	// Prune the now-empty per-file (<hash>) and per-server parents.
	// os.Remove only succeeds on empty directories, so concurrent sessions are left intact.
	hashDir := filepath.Dir(sessionDir)
	serverDir := filepath.Dir(hashDir)
	_ = os.Remove(hashDir)
	_ = os.Remove(serverDir)
}

func normalizeEditDeps(deps editDeps) editDeps {
	if deps.runEditor == nil {
		deps.runEditor = runLocalEditor
	}
	if deps.confirmLarge == nil {
		deps.confirmLarge = func(int64) bool { return true }
	}
	if deps.removeAll == nil {
		deps.removeAll = os.RemoveAll
	}
	return deps
}

func editTempPath(root string, target editTarget) (string, error) {
	base, err := utils.RemoteFileName(target.RemotePath)
	if err != nil {
		return "", err
	}
	if root == "" {
		root = defaultEditRoot()
	}
	sum := sha256.Sum256([]byte(target.Server + ":" + target.RemotePath))
	parent := filepath.Join(root, sanitizePathPart(target.Server), hex.EncodeToString(sum[:])[:16])
	if err := os.MkdirAll(parent, 0700); err != nil {
		return "", err
	}
	dir, err := os.MkdirTemp(parent, "session-")
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, base), nil
}

func defaultEditRoot() string {
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".alpacon", "edit")
	}
	return filepath.Join(os.TempDir(), "alpacon-edit")
}

func sanitizePathPart(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' {
			return r
		}
		return '_'
	}, s)
}

func hashFile(filePath string) ([32]byte, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return [32]byte{}, err
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return [32]byte{}, err
	}
	var sum [32]byte
	copy(sum[:], h.Sum(nil))
	return sum, nil
}

func runLocalEditor(editor, filePath string) error {
	parts, err := splitEditorCommand(editor)
	if err != nil {
		return err
	}
	cmd := exec.Command(parts[0], append(parts[1:], filePath)...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

func splitEditorCommand(command string) ([]string, error) {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil, fmt.Errorf("editor command is empty")
	}

	var parts []string
	var current strings.Builder
	var quote rune
	escaped := false
	argStarted := false
	for _, r := range command {
		switch {
		case escaped:
			if r != '\\' && r != '\'' && r != '"' && r != ' ' && r != '\t' && r != '\n' {
				current.WriteRune('\\')
			}
			current.WriteRune(r)
			escaped = false
			argStarted = true
		case r == '\\':
			escaped = true
			argStarted = true
		case quote != 0:
			if r == quote {
				quote = 0
			} else {
				current.WriteRune(r)
				argStarted = true
			}
		case r == '\'' || r == '"':
			quote = r
			argStarted = true
		case r == ' ' || r == '\t' || r == '\n':
			if argStarted {
				parts = append(parts, current.String())
				current.Reset()
				argStarted = false
			}
		default:
			current.WriteRune(r)
			argStarted = true
		}
	}
	if escaped {
		current.WriteRune('\\')
	}
	if quote != 0 {
		return nil, fmt.Errorf("editor command has unmatched quote")
	}
	if argStarted {
		parts = append(parts, current.String())
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("editor command is empty")
	}
	return parts, nil
}

func confirmLargeEdit(size int64) bool {
	if !utils.IsInteractiveShell() {
		return false
	}
	return utils.PromptForBool(fmt.Sprintf("Remote file is %s. Edit anyway?", formatBytes(size)))
}

// formatBytes renders a size in MB; its only caller guards on size > 10 MB.
func formatBytes(size int64) string {
	const mb = 1024 * 1024
	return fmt.Sprintf("%.1f MB", float64(size)/float64(mb))
}

func printPreservedTempPath(result editResult) {
	// Only claim preservation when the user actually changed the file; an
	// unchanged temp is either already cleaned up or just a copy of the remote.
	if !result.Changed || result.TempPath == "" {
		return
	}
	if _, err := os.Stat(result.TempPath); err == nil {
		utils.CliWarning("Edited file preserved at %s", result.TempPath)
	}
}
