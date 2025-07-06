package ftp

import (
	"fmt"
	"strings"

	"github.com/alpacax/alpacon-cli/api/ftp"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
)

var CpCmd = &cobra.Command{
	Use:   "cp [SOURCE...] [DESTINATION]",
	Short: "Copy files between local and remote locations",
	Long: `The cp command allows you to copy files between your local machine and a remote server.
	Copy files between your local machine and a remote server using the cp command.
	This command supports uploading, downloading, and specifying authentication details
	such as username and groupname.
	
	Example usages:
	- To upload multiple files to a remote server:
	  alpacon cp /local/path/file1.txt /local/path/file2.txt [SERVER_NAME]:/remote/path/

	- To upload or download directory:
	  alpacon cp -r /local/path/directory [SERVER_NAME]:/remote/path/
	  alpacon cp -r [SERVER_NAME]:/remote/path/directory /local/path/

	- To download files from a remote server to a local destination:
	  alpacon cp [SERVER_NAME]:/remote/path1 /remote/path2 /local/destination/path

	- To specify username:
	  alpacon cp /local/path/file.txt [USER_NAME]@[SERVER_NAME]:/remote/path/
	  alpacon cp -u [USER_NAME] /local/path/file.txt [SERVER_NAME]:/remote/path/

	- To specify groupname:
	  alpacon cp -g [GROUP_NAME] /local/path/file.txt [SERVER_NAME]:/remote/path/
	`,
	Run: func(cmd *cobra.Command, args []string) {
		username, _ := cmd.Flags().GetString("username")
		groupname, _ := cmd.Flags().GetString("groupname")
		recursive, _ := cmd.Flags().GetBool("recursive")

		if len(args) < 2 {
			utils.CliError("You must specify at least two arguments.")
			return
		}

		for i, arg := range args {
			if strings.Contains(arg, "@") && strings.Contains(arg, ":") {
				parts := strings.SplitN(arg, "@", 2)
				if username == "" {
					username = parts[0]
				}
				// Remove the username@ part from the argument
				args[i] = parts[1]
			}
		}

		sources := args[:len(args)-1]
		dest := args[len(args)-1]

		alpaconClient, err := client.NewAlpaconAPIClient()
		if err != nil {
			utils.CliError("Connection to Alpacon API failed: %s. Consider re-logging.", err)
			return
		}

		if isLocalPaths(sources) && isRemotePath(dest) {
			uploadObject(alpaconClient, sources, dest, username, groupname, recursive)
		} else if isRemotePath(sources[0]) && isLocalPath(dest) {
			downloadObject(alpaconClient, sources[0], dest, username, groupname, recursive)
		} else {
			utils.CliError("Invalid combination of source and destination paths.")
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
	return strings.Contains(path, ":")
}

// isLocalPath determines if the given path is a local file system path.
func isLocalPath(path string) bool {
	return !isRemotePath(path)
}

func isLocalPaths(paths []string) bool {
	for _, path := range paths {
		if isRemotePath(path) {
			return false
		}
	}
	return true
}

func uploadObject(client *client.AlpaconClient, src []string, dest, username, groupname string, recursive bool) {
	var result []string
	var err error

	if recursive {
		result, err = ftp.UploadFolder(client, src, dest, username, groupname)
	} else {
		result, err = ftp.UploadFile(client, src, dest, username, groupname)
	}
	if err != nil {
		utils.CliError("Failed to upload the file to server: %s.", err)
	}
	wrappedSrc := fmt.Sprintf("[%s]", strings.Join(src, ", "))
	utils.CliInfo("Upload request for %s to %s successful.", wrappedSrc, dest)
	fmt.Printf("Result: %s.\n", result)
}

func downloadObject(client *client.AlpaconClient, src, dest, username, groupname string, recursive bool) {
	var err error
	err = ftp.DownloadFile(client, src, dest, username, groupname, recursive)

	if err != nil {
		utils.CliError("Failed to download the file from server: %s.", err)
	}
	utils.CliInfo("Download request for %s to server %s successful.", src, dest)
}
