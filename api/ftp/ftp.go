package ftp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/google/uuid"
)

const (
	uploadAPIURL   = "/api/webftp/uploads/"
	downloadAPIURL = "/api/webftp/downloads/"

	// Polling configuration for transfer status
	pollInterval   = 2 * time.Second
	maxPollRetries = 15 // 1 minute timeout (pollInterval * maxPollRetries)
)

// PollTransferStatus polls the transfer status API until success/failure or timeout.
// transferType should be "upload" or "download", id is the transfer ID.
// Returns true if transfer succeeded, false if failed, and error if polling timed out or failed.
func PollTransferStatus(ac *client.AlpaconClient, transferType, id string) (bool, string, error) {
	var statusURL string
	if transferType == "upload" {
		statusURL = fmt.Sprintf("%s%s/status/", uploadAPIURL, id)
	} else {
		statusURL = fmt.Sprintf("%s%s/status/", downloadAPIURL, id)
	}

	for i := 0; i < maxPollRetries; i++ {
		respBody, err := ac.SendGetRequest(statusURL)
		if err != nil {
			// Check if it's a 422 error (transfer in progress)
			if strings.Contains(err.Error(), "webftp_transfer_in_progress") {
				time.Sleep(pollInterval)
				continue
			}
			return false, "", fmt.Errorf("failed to check transfer status: %w", err)
		}

		var statusResp TransferStatusResponse
		if err := json.Unmarshal(respBody, &statusResp); err != nil {
			return false, statusResp.Message, fmt.Errorf("failed to parse transfer status response: %w", err)
		}

		if statusResp.Success != nil {
			return *statusResp.Success, statusResp.Message, nil
		}

		time.Sleep(pollInterval)
	}

	return false, "", fmt.Errorf("transfer status polling timed out after 1 minute")
}

func uploadToS3(uploadUrl string, file io.Reader) error {
	req, err := http.NewRequest(http.MethodPut, uploadUrl, file)
	if err != nil {
		return err
	}

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return err
	}
	return nil
}

func executeUpload(ac *client.AlpaconClient, uploadRequest *UploadRequest, content []byte) error {
	respBody, err := ac.SendPostRequest(uploadAPIURL, uploadRequest)
	if err != nil {
		return err
	}

	var response UploadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if response.UploadUrl != "" {
		if err := uploadToS3(response.UploadUrl, bytes.NewReader(content)); err != nil {
			return err
		}
	}

	relativePath := path.Join(response.Id, "upload")
	fullURL := utils.BuildURL(uploadAPIURL, relativePath, nil)
	if _, err := ac.SendGetRequest(fullURL); err != nil {
		return err
	}

	success, message, err := PollTransferStatus(ac, "upload", response.Id)
	if err != nil {
		return fmt.Errorf("upload transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

func uploadSingleFile(ac *client.AlpaconClient, filePath, remotePath, serverID, username, groupname string) error {
	file, err := utils.ReadFileFromPath(filePath)
	if err != nil {
		return err
	}

	uploadRequest := &UploadRequest{
		Id:             uuid.New().String(),
		Name:           filepath.Base(filePath),
		Path:           remotePath,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: "true",
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Uploading %s ...", filepath.Base(filePath)))
	spinner.Start()
	defer spinner.Stop()

	return executeUpload(ac, uploadRequest, file)
}

func UploadFile(ac *client.AlpaconClient, src []string, dest, username, groupname string) error {
	serverName, remotePath := utils.SplitPath(dest)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	for _, filePath := range src {
		if err := uploadSingleFile(ac, filePath, remotePath, serverID, username, groupname); err != nil {
			return err
		}
	}

	return nil
}

func uploadSingleFolder(ac *client.AlpaconClient, folderPath, remotePath, serverID, username, groupname string) error {
	zipBytes, err := utils.Zip(folderPath)
	if err != nil {
		return err
	}

	uploadRequest := &UploadRequest{
		Id:             uuid.New().String(),
		AllowUnzip:     "true",
		AllowOverwrite: "true",
		Name:           filepath.Base(folderPath) + ".zip",
		Path:           remotePath,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Uploading %s...", filepath.Base(folderPath)))
	spinner.Start()
	defer spinner.Stop()

	return executeUpload(ac, uploadRequest, zipBytes)
}

func UploadFolder(ac *client.AlpaconClient, src []string, dest, username, groupname string) error {
	serverName, remotePath := utils.SplitPath(dest)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	for _, folderPath := range src {
		if err := uploadSingleFolder(ac, folderPath, remotePath, serverID, username, groupname); err != nil {
			return err
		}
	}

	return nil
}

func fetchFromURL(url string, maxAttempts int) ([]byte, error) {
	var resp *http.Response
	var err error

	for count := 0; count < maxAttempts; count++ {
		resp, err = http.Get(url)
		if err != nil {
			return nil, fmt.Errorf("network error while downloading: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}
		time.Sleep(time.Second * 1)

		if count == maxAttempts-1 {
			return nil, fmt.Errorf("download failed after %d attempts", maxAttempts)
		}
	}

	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(resp.Body)
}

func saveDownloadedContent(content []byte, dest, remotePath string, recursive bool) error {
	var fileName string
	if recursive {
		fileName = filepath.Base(remotePath) + ".zip"
	} else {
		fileName = filepath.Base(remotePath)
	}

	filePath := filepath.Join(dest, fileName)
	if err := utils.SaveFile(filePath, content); err != nil {
		return fmt.Errorf("failed to save file locally: %w", err)
	}

	if recursive {
		if err := utils.Unzip(filePath, dest); err != nil {
			return fmt.Errorf("failed to extract downloaded folder: %w", err)
		}
		if err := utils.DeleteFile(filePath); err != nil {
			return fmt.Errorf("failed to clean up temporary zip file: %w", err)
		}
	}

	return nil
}

func downloadSingleFile(ac *client.AlpaconClient, remotePath, dest, serverID, username, groupname, resourceType string, recursive bool) error {
	downloadRequest := &DownloadRequest{
		Path:         remotePath,
		Name:         filepath.Base(remotePath),
		Server:       serverID,
		Username:     username,
		Groupname:    groupname,
		ResourceType: resourceType,
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Downloading %s...", filepath.Base(remotePath)))
	spinner.Start()
	defer spinner.Stop()

	postBody, err := ac.SendPostRequest(downloadAPIURL, downloadRequest)
	if err != nil {
		return err
	}

	var downloadResponse DownloadResponse
	if err := json.Unmarshal(postBody, &downloadResponse); err != nil {
		return err
	}

	status, err := event.PollCommandExecution(ac, downloadResponse.Command)
	if err != nil {
		return err
	}

	if status.Status["text"] == "Stuck" || status.Status["text"] == "Error" {
		utils.CliErrorWithExit("%s", status.Status["message"].(string))
	}
	if status.Status["text"] == "Failed" {
		utils.CliErrorWithExit("%s", status.Result)
	}

	content, err := fetchFromURL(downloadResponse.DownloadURL, 100)
	if err != nil {
		return err
	}

	if err := saveDownloadedContent(content, dest, remotePath, recursive); err != nil {
		return err
	}

	success, message, err := PollTransferStatus(ac, "download", downloadResponse.ID)
	if err != nil {
		return fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

func DownloadFile(ac *client.AlpaconClient, src, dest, username, groupname string, recursive bool) error {
	serverName, remotePathStr := utils.SplitPath(src)

	trimmedPathStr := strings.Trim(remotePathStr, "\"")
	remotePaths := strings.Fields(trimmedPathStr)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	resourceType := "file"
	if recursive {
		resourceType = "folder"
	}

	for _, remotePath := range remotePaths {
		if err := downloadSingleFile(ac, remotePath, dest, serverID, username, groupname, resourceType, recursive); err != nil {
			return err
		}
	}

	return nil
}
