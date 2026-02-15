package ftp

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/api/iam"
	"github.com/alpacax/alpacon-cli/api/mfa"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var CpCmd = &cobra.Command{
	Use:   "cp [SOURCE...] [DESTINATION]",
	Short: "Copy files between local and remote locations",
	Long: `Copy files between your local machine and a remote server.
Supports SSH-like user@host:path syntax for specifying the username inline with the remote path.
Remote paths use the format [USER@]SERVER:/path.`,
	Example: `  # Upload files to a remote server
  alpacon cp /local/file1.txt /local/file2.txt my-server:/remote/path/

  # Upload or download a directory
  alpacon cp -r /local/directory my-server:/remote/path/
  alpacon cp -r my-server:/remote/directory /local/path/

  # Download a file from a remote server
  alpacon cp my-server:/remote/file.txt /local/path/

  # Specify username with SSH-like syntax
  alpacon cp /local/file.txt admin@my-server:/remote/path/
  alpacon cp -r admin@my-server:/var/log/ /local/logs/

  # Specify username with flag
  alpacon cp -u admin /local/file.txt my-server:/remote/path/

  # Specify groupname
  alpacon cp -g developers /local/file.txt my-server:/remote/path/`,
	Run: func(cmd *cobra.Command, args []string) {
		username, _ := cmd.Flags().GetString("username")
		groupname, _ := cmd.Flags().GetString("groupname")
		recursive, _ := cmd.Flags().GetBool("recursive")

		if len(args) < 2 {
			utils.CliErrorWithExit("You must specify at least two arguments.\n\n" +
				"Usage examples:\n" +
				"  • Upload file to server:\n" +
				"    alpacon cp /local/file.txt server:/remote/path/\n" +
				"  • Download file from server:\n" +
				"    alpacon cp server:/remote/file.txt /local/path/\n" +
				"  • Upload folder (recursive):\n" +
				"    alpacon cp -r /local/folder server:/remote/path/\n\n" +
				"Note: Remote paths must include server name (e.g., myserver:/path/)")
			return
		}

		for i, arg := range args {
			if strings.Contains(arg, "@") && (strings.Contains(arg, ":") || !utils.IsRemoteTarget(arg)) {
				// Parse SSH-like target: user@host or user@host:path
				sshTarget := utils.ParseSSHTarget(arg)
				if username == "" && sshTarget.User != "" {
					username = sshTarget.User
				}
				// Reconstruct the argument without the user part
				if sshTarget.Path != "" {
					args[i] = sshTarget.Host + ":" + sshTarget.Path
				} else {
					args[i] = sshTarget.Host
				}
			}
		}

		sources := args[:len(args)-1]
		dest := args[len(args)-1]

		// Validate source and destination paths
		if err := validatePaths(sources, dest); err != nil {
			utils.CliErrorWithExit("%s", err.Error())
			return
		}

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliErrorWithExit("Connection to Alpacon API failed: %s.\n\n"+
				"Try these solutions:\n"+
				"  • Re-login with 'alpacon login'\n"+
				"  • Check your internet connection\n"+
				"  • Verify the API endpoint is accessible", err)
			return
		}

		if isLocalPaths(sources) && isRemotePath(dest) {
			serverName, _ := utils.SplitPath(dest)
			err := uploadObject(alpaconClient, sources, dest, username, groupname, recursive)
			if err != nil {
				err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
					OnMFARequired: func(srv string) error {
						return mfa.HandleMFAError(alpaconClient, srv)
					},
					OnUsernameRequired: func() error {
						_, err := iam.HandleUsernameRequired()
						return err
					},
					RetryOperation: func() error {
						return uploadObject(alpaconClient, sources, dest, username, groupname, recursive)
					},
				})

				if err != nil {
					// Error already handled in uploadObject
					return
				}
			}
		} else if isRemotePath(sources[0]) && isLocalPath(dest) {
			serverName, _ := utils.SplitPath(sources[0])
			err := downloadObject(alpaconClient, sources[0], dest, username, groupname, recursive)
			if err != nil {
				err = utils.HandleCommonErrors(err, serverName, utils.ErrorHandlerCallbacks{
					OnMFARequired: func(srv string) error {
						return mfa.HandleMFAError(alpaconClient, srv)
					},
					OnUsernameRequired: func() error {
						_, err := iam.HandleUsernameRequired()
						return err
					},
					RetryOperation: func() error {
						return downloadObject(alpaconClient, sources[0], dest, username, groupname, recursive)
					},
				})

				if err != nil {
					// Error already handled in downloadObject
					return
				}
			}
		} else {
			utils.CliErrorWithExit("Invalid combination of source and destination paths.\n\n" +
				"Valid operations:\n" +
				"  • Upload (local → remote): alpacon cp /local/file server:/remote/path/\n" +
				"  • Download (remote → local): alpacon cp server:/remote/file /local/path/\n\n" +
				"Note: Remote paths must be in format 'servername:/path' (e.g., myserver:/tmp/file.txt)")
		}
	},
}

func init() {
	var username, groupname string

	CpCmd.Flags().BoolP("recursive", "r", false, "Recursively copy directories")
	CpCmd.Flags().StringVarP(&username, "username", "u", "", "Specify username")
	CpCmd.Flags().StringVarP(&groupname, "groupname", "g", "", "Specify groupname")
}

// isRemotePath determines if the given path is a remote server path.
func isRemotePath(path string) bool {
	return utils.IsRemoteTarget(path)
}

// isLocalPath determines if the given path is a local file system path.
func isLocalPath(path string) bool {
	return utils.IsLocalTarget(path)
}

func isLocalPaths(paths []string) bool {
	for _, path := range paths {
		if isRemotePath(path) {
			return false
		}
	}
	return true
}

func validatePaths(sources []string, dest string) error {
	// Check for mixed local and remote sources (not allowed)
	hasLocal := false
	hasRemote := false

	for _, src := range sources {
		if isRemotePath(src) {
			hasRemote = true
		} else {
			hasLocal = true
		}
	}

	if hasLocal && hasRemote {
		return fmt.Errorf("cannot mix local and remote source paths in a single operation.\n\n" +
			"Examples of valid operations:\n" +
			"  • Upload multiple local files: alpacon cp file1.txt file2.txt server:/remote/\n" +
			"  • Download single remote file: alpacon cp server:/remote/file.txt /local/\n" +
			"  • Cannot mix: alpacon cp file1.txt server:/file2.txt /dest/  ❌")
	}

	// Check for invalid remote path format
	allPaths := append(sources, dest)
	for _, path := range allPaths {
		if isRemotePath(path) {
			if !strings.Contains(path, ":") {
				return fmt.Errorf("invalid remote path format: '%s'\n\n"+
					"Remote paths must be in format 'servername:/path'\n"+
					"Examples:\n"+
					"  • myserver:/home/user/file.txt\n"+
					"  • web-server:/var/www/index.html", path)
			}

			parts := strings.SplitN(path, ":", 2)
			if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
				return fmt.Errorf("invalid remote path format: '%s'\n\n"+
					"Remote paths must include both server name and path:\n"+
					"  • Correct: myserver:/path/to/file\n"+
					"  • Incorrect: :myfile (missing server name)\n"+
					"  • Incorrect: myserver: (missing path)", path)
			}
		}
	}

	return nil
}

func uploadObject(client *client.AlpaconClient, src []string, dest, username, groupname string, recursive bool) error {
	var err error

	// Extract server name for better error messages
	serverName, remotePath := utils.SplitPath(dest)

	if recursive {
		err = ftp.UploadFolder(client, src, dest, username, groupname)
	} else {
		err = ftp.UploadFile(client, src, dest, username, groupname)
	}
	if err != nil {
		// Parse error and provide specific guidance
		errStr := err.Error()
		if strings.Contains(errStr, "no such file or directory") {
			utils.CliErrorWithExit("Source file(s) not found: %s\n\n"+
				"Please check:\n"+
				"  • File paths are correct and files exist\n"+
				"  • You have read permissions for the source files\n"+
				"  • For folders, use -r flag: alpacon cp -r /local/folder %s",
				strings.Join(src, ", "), dest)
		} else if strings.Contains(errStr, "permission denied") || strings.Contains(errStr, "access denied") {
			utils.CliErrorWithExit("Permission denied uploading to '%s' on server '%s'.\n\n"+
				"Try these solutions:\n"+
				"  • Upload as root: alpacon cp -u root %s %s\n"+
				"  • Upload to writable location: alpacon cp %s %s:/tmp/\n"+
				"  • Check destination directory permissions\n"+
				"  • Ensure destination directory exists",
				remotePath, serverName, strings.Join(src, " "), dest, strings.Join(src, " "), serverName)
		} else if strings.Contains(errStr, "server not found") || strings.Contains(errStr, "unknown host") {
			utils.CliErrorWithExit("Server '%s' not found.\n\n"+
				"Please check:\n"+
				"  • Server name is spelled correctly\n"+
				"  • Server is registered: alpacon server ls\n"+
				"  • You have access to this server", serverName)
		} else {
			utils.CliError("Failed to upload to server '%s': %s\n\n"+
				"Try these solutions:\n"+
				"  • Check server connectivity: alpacon exec %s 'echo test'\n"+
				"  • Verify destination path exists: alpacon exec %s 'ls -la %s'\n"+
				"  • Check available disk space on server",
				serverName, err, serverName, serverName, filepath.Dir(remotePath))
		}
		return err
	}
	wrappedSrc := fmt.Sprintf("[%s]", strings.Join(src, ", "))
	utils.CliInfo("Upload request for %s to %s successful.", wrappedSrc, dest)
	return nil
}

func downloadObject(client *client.AlpaconClient, src, dest, username, groupname string, recursive bool) error {
	// Extract server name for better error messages
	serverName, remotePath := utils.SplitPath(src)

	err := ftp.DownloadFile(client, src, dest, username, groupname, recursive)
	if err != nil {
		// Parse error and provide specific guidance
		errStr := err.Error()
		if strings.Contains(errStr, "no such file or directory") || strings.Contains(errStr, "file not found") {
			utils.CliErrorWithExit("Source file not found: '%s' on server '%s'.\n\n"+
				"Please check:\n"+
				"  • File path is correct: %s\n"+
				"  • File exists: alpacon exec %s 'ls -la %s'\n"+
				"  • You have read permissions for the file\n"+
				"  • For folders, use -r flag: alpacon cp -r %s %s",
				remotePath, serverName, remotePath, serverName, filepath.Dir(remotePath), src, dest)
		} else if strings.Contains(errStr, "permission denied") || strings.Contains(errStr, "access denied") {
			utils.CliErrorWithExit("Permission denied downloading '%s' from server '%s'.\n\n"+
				"Try these solutions:\n"+
				"  • Download as root: alpacon cp -u root %s %s\n"+
				"  • Download as file owner: alpacon cp -u OWNER %s %s\n"+
				"  • Check file permissions: alpacon exec %s 'ls -la %s'",
				remotePath, serverName, src, dest, src, dest, serverName, remotePath)
		} else if strings.Contains(errStr, "server not found") || strings.Contains(errStr, "unknown host") {
			utils.CliErrorWithExit("Server '%s' not found.\n\n"+
				"Please check:\n"+
				"  • Server name is spelled correctly\n"+
				"  • Server is registered: alpacon server ls\n"+
				"  • You have access to this server", serverName)
		} else if strings.Contains(errStr, "download failed") {
			utils.CliErrorWithExit("Download failed from server '%s': %s\n\n"+
				"This might be due to:\n"+
				"  • Network connectivity issues\n"+
				"  • Server timeout (file too large)\n"+
				"  • Insufficient local disk space\n"+
				"  • Server-side file access issues",
				serverName, err)
		} else {
			utils.CliError("Failed to download from server '%s': %s\n\n"+
				"Try these solutions:\n"+
				"  • Check server connectivity: alpacon exec %s 'echo test'\n"+
				"  • Verify source file: alpacon exec %s 'file %s'\n"+
				"  • Check local destination permissions",
				serverName, err, serverName, serverName, remotePath)
		}
		return err
	}
	utils.CliInfo("Download request for %s to %s successful.", src, dest)
	return nil
}
