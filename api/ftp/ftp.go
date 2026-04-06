package ftp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	uploadAPIURL         = "/api/webftp/uploads/"
	uploadBulkAPIURL     = "/api/webftp/uploads/bulk/"
	uploadBulkTriggerURL = "/api/webftp/uploads/bulk-upload/"
	downloadAPIURL       = "/api/webftp/downloads/"
	downloadBulkAPIURL   = "/api/webftp/downloads/bulk/"

	// Polling configuration for transfer status
	pollInterval       = 2 * time.Second
	basePollTimeout    = 30 * time.Second
	perFilePollTimeout = 10 * time.Second
	perMBPollTimeout   = 5 * time.Second
)

// calcPollTimeout returns a dynamic poll timeout based on file count and total size.
// Base 30s + 10s per file + 5s per MB.
func calcPollTimeout(fileCount int, totalBytes int64) time.Duration {
	timeout := basePollTimeout +
		time.Duration(fileCount)*perFilePollTimeout +
		time.Duration(totalBytes/(1024*1024))*perMBPollTimeout
	return timeout
}

// PollTransferStatus polls the transfer status API until success/failure or timeout.
// transferType should be "upload" or "download", id is the transfer ID.
// timeout controls how long to poll before giving up.
// Returns true if transfer succeeded, false if failed, and error if polling timed out or failed.
func PollTransferStatus(ac *client.AlpaconClient, transferType, id string, timeout time.Duration) (bool, string, error) {
	var statusURL string
	if transferType == "upload" {
		statusURL = fmt.Sprintf("%s%s/status/", uploadAPIURL, id)
	} else {
		statusURL = fmt.Sprintf("%s%s/status/", downloadAPIURL, id)
	}

	maxRetries := int(timeout / pollInterval)
	if maxRetries < 1 {
		maxRetries = 1
	}

	for i := 0; i < maxRetries; i++ {
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

	return false, "", fmt.Errorf("transfer status polling timed out after %v", timeout)
}

func uploadToS3(httpClient *http.Client, uploadURL string, file io.Reader) error {
	req, err := http.NewRequest(http.MethodPut, uploadURL, file)
	if err != nil {
		return err
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload failed with status %d", resp.StatusCode)
	}
	return nil
}

func executeSingleUpload(ac *client.AlpaconClient, request *UploadRequest, content []byte) error {
	respBody, err := ac.SendPostRequest(uploadAPIURL, request)
	if err != nil {
		return err
	}

	var response UploadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if response.UploadURL != "" {
		if err := uploadToS3(ac.HTTPClient, response.UploadURL, bytes.NewReader(content)); err != nil {
			return err
		}
	}

	triggerURL := utils.BuildURL(uploadAPIURL, fmt.Sprintf("%s/upload", response.ID), nil)
	if _, err := ac.SendGetRequest(triggerURL); err != nil {
		return err
	}

	timeout := calcPollTimeout(1, int64(len(content)))
	success, message, err := PollTransferStatus(ac, "upload", response.ID, timeout)
	if err != nil {
		return fmt.Errorf("upload transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

func executeBulkUpload(ac *client.AlpaconClient, request *BulkUploadRequest, contents [][]byte) error {
	respBody, err := ac.SendPostRequest(uploadBulkAPIURL, request)
	if err != nil {
		return err
	}

	var responses []UploadResponse
	if err := json.Unmarshal(respBody, &responses); err != nil {
		return err
	}

	if len(responses) != len(contents) {
		return fmt.Errorf("server returned %d upload slots but %d files were provided", len(responses), len(contents))
	}

	// Upload each file to S3
	ids := make([]string, 0, len(responses))
	for i, resp := range responses {
		ids = append(ids, resp.ID)
		if resp.UploadURL != "" {
			if err := uploadToS3(ac.HTTPClient, resp.UploadURL, bytes.NewReader(contents[i])); err != nil {
				return fmt.Errorf("failed to upload %s to storage: %w", resp.Name, err)
			}
		}
	}

	// Trigger server-side processing
	triggerRequest := &BulkUploadTriggerRequest{IDs: ids}
	if _, err := ac.SendPostRequest(uploadBulkTriggerURL, triggerRequest); err != nil {
		return err
	}

	// Poll transfer status for each upload
	var totalBytes int64
	for _, c := range contents {
		totalBytes += int64(len(c))
	}
	timeout := calcPollTimeout(len(contents), totalBytes)

	for _, resp := range responses {
		success, message, err := PollTransferStatus(ac, "upload", resp.ID, timeout)
		if err != nil {
			return fmt.Errorf("upload transfer status check failed for %s: %w", resp.Name, err)
		}
		if !success {
			return fmt.Errorf("upload failed for %s: %s", resp.Name, message)
		}
	}

	return nil
}

// UploadFile uploads local files to a remote server.
// Uses the single upload API for one file, or the bulk API for multiple files.
func UploadFile(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool) error {
	serverName, remotePath := utils.SplitPath(dest)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	if len(src) == 1 {
		content, err := utils.ReadFileFromPath(src[0])
		if err != nil {
			return err
		}

		spinner := utils.NewSpinner(fmt.Sprintf("Uploading %s...", filepath.Base(src[0])))
		spinner.Start()
		defer spinner.Stop()

		request := &UploadRequest{
			Name:           filepath.Base(src[0]),
			Path:           remotePath,
			Server:         serverID,
			Username:       username,
			Groupname:      groupname,
			AllowOverwrite: allowOverwrite,
		}
		return executeSingleUpload(ac, request, content)
	}

	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	names := make([]string, len(src))
	contents := make([][]byte, len(src))
	for i, filePath := range src {
		content, err := utils.ReadFileFromPath(filePath)
		if err != nil {
			return err
		}
		names[i] = filepath.Base(filePath)
		contents[i] = content
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Uploading %d files...", len(src)))
	spinner.Start()
	defer spinner.Stop()

	request := &BulkUploadRequest{
		Names:          names,
		Path:           remotePath,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: allowOverwrite,
	}

	return executeBulkUpload(ac, request, contents)
}

// UploadFolder uploads local folders to a remote server.
// Each folder is zipped before upload and extracted on the server side.
// Uses the single upload API for one folder, or the bulk API for multiple folders.
func UploadFolder(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool) error {
	serverName, remotePath := utils.SplitPath(dest)

	// Folder uploads always target a directory; ensure trailing slash so the
	// server recognises the path as a directory.
	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	if len(src) == 1 {
		zipBytes, err := utils.Zip(src[0])
		if err != nil {
			return err
		}

		spinner := utils.NewSpinner(fmt.Sprintf("Uploading %s...", filepath.Base(src[0])))
		spinner.Start()
		defer spinner.Stop()

		request := &UploadRequest{
			Name:           filepath.Base(src[0]) + ".zip",
			Path:           remotePath,
			Server:         serverID,
			Username:       username,
			Groupname:      groupname,
			AllowOverwrite: allowOverwrite,
			AllowUnzip:     true,
		}
		return executeSingleUpload(ac, request, zipBytes)
	}

	names := make([]string, len(src))
	contents := make([][]byte, len(src))
	for i, folderPath := range src {
		zipBytes, err := utils.Zip(folderPath)
		if err != nil {
			return err
		}
		names[i] = filepath.Base(folderPath) + ".zip"
		contents[i] = zipBytes
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Uploading %d folders...", len(src)))
	spinner.Start()
	defer spinner.Stop()

	request := &BulkUploadRequest{
		Names:          names,
		Path:           remotePath,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: allowOverwrite,
		AllowUnzip:     true,
	}

	return executeBulkUpload(ac, request, contents)
}

func fetchFromURL(httpClient *http.Client, url string, maxAttempts int) ([]byte, error) {
	var resp *http.Response
	var err error

	for count := 0; count < maxAttempts; count++ {
		resp, err = httpClient.Get(url)
		if err != nil {
			return nil, fmt.Errorf("network error while downloading: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}
		_ = resp.Body.Close()

		if count == maxAttempts-1 {
			return nil, fmt.Errorf("download failed after %d attempts", maxAttempts)
		}
		time.Sleep(time.Second * 1)
	}

	defer func() { _ = resp.Body.Close() }()

	return io.ReadAll(resp.Body)
}

func saveDownloadedContent(content []byte, dest, remotePath string, recursive bool) error {
	var filePath string
	if recursive {
		fileName := filepath.Base(remotePath) + ".zip"
		filePath = filepath.Join(dest, fileName)
	} else {
		// If dest is an existing directory or ends with a separator, append the remote filename.
		// Otherwise treat dest as the target file path directly (cp-style rename semantics).
		info, err := os.Stat(dest)
		if err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("failed to access destination %q: %w", dest, err)
		}
		destHasTrailingSep := len(dest) > 0 && os.IsPathSeparator(dest[len(dest)-1])
		if (err == nil && info.IsDir()) || destHasTrailingSep {
			filePath = filepath.Join(dest, filepath.Base(remotePath))
		} else {
			filePath = dest
		}
	}

	if err := utils.SaveFile(filePath, content); err != nil {
		return fmt.Errorf("failed to save file locally: %w", err)
	}

	if recursive {
		defer func() { _ = utils.DeleteFile(filePath) }()
		if err := utils.Unzip(filePath, dest); err != nil {
			return fmt.Errorf("failed to extract downloaded folder: %w", err)
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

	if status.Status == "stuck" || status.Status == "error" {
		return fmt.Errorf("command failed with status: %s", status.Status)
	}
	if status.Status == "failed" {
		return fmt.Errorf("%s", status.Result)
	}

	content, err := fetchFromURL(ac.HTTPClient, downloadResponse.DownloadURL, 100)
	if err != nil {
		return err
	}

	if err := saveDownloadedContent(content, dest, remotePath, recursive); err != nil {
		return err
	}

	timeout := calcPollTimeout(1, int64(len(content)))
	success, message, err := PollTransferStatus(ac, "download", downloadResponse.ID, timeout)
	if err != nil {
		return fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

// downloadBulk downloads multiple remote files as a single zip archive using the bulk API.
func downloadBulk(ac *client.AlpaconClient, remotePaths []string, dest, serverID, username, groupname string) error {
	spinner := utils.NewSpinner(fmt.Sprintf("Downloading %d files...", len(remotePaths)))
	spinner.Start()
	defer spinner.Stop()

	request := &BulkDownloadRequest{
		Path:      remotePaths,
		Server:    serverID,
		Username:  username,
		Groupname: groupname,
	}

	respBody, err := ac.SendPostRequest(downloadBulkAPIURL, request)
	if err != nil {
		return err
	}

	var response BulkDownloadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	status, err := event.PollCommandExecution(ac, response.Command)
	if err != nil {
		return err
	}

	if status.Status == "stuck" || status.Status == "error" {
		return fmt.Errorf("command failed with status: %s", status.Status)
	}
	if status.Status == "failed" {
		return fmt.Errorf("%s", status.Result)
	}

	content, err := fetchFromURL(ac.HTTPClient, response.DownloadURL, 100)
	if err != nil {
		return err
	}

	// Save zip and extract contents
	zipPath := filepath.Join(dest, filepath.Base(response.Name))
	if err := utils.SaveFile(zipPath, content); err != nil {
		return fmt.Errorf("failed to save downloaded archive: %w", err)
	}
	defer func() { _ = utils.DeleteFile(zipPath) }()

	if err := utils.Unzip(zipPath, dest); err != nil {
		return fmt.Errorf("failed to extract downloaded archive: %w", err)
	}

	timeout := calcPollTimeout(len(remotePaths), int64(len(content)))
	success, message, err := PollTransferStatus(ac, "download", response.ID, timeout)
	if err != nil {
		return fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

// DownloadFile downloads files from a remote server. Uses the bulk API for multiple files,
// or the single-file API for a single file.
func DownloadFile(ac *client.AlpaconClient, src, dest, username, groupname string, recursive bool) error {
	serverName, remotePathStr := utils.SplitPath(src)

	trimmedPathStr := strings.Trim(remotePathStr, "\"")
	remotePaths := strings.Fields(trimmedPathStr)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	if len(remotePaths) > 1 {
		return downloadBulk(ac, remotePaths, dest, serverID, username, groupname)
	}

	resourceType := "file"
	if recursive {
		resourceType = "folder"
	}

	return downloadSingleFile(ac, remotePaths[0], dest, serverID, username, groupname, resourceType, recursive)
}
