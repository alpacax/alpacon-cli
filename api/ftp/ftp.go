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
	"sync"
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
	uploadStatusURL      = "/api/webftp/uploads/%s/status/"
	downloadAPIURL       = "/api/webftp/downloads/"
	downloadBulkAPIURL   = "/api/webftp/downloads/bulk/"
	downloadStatusURL    = "/api/webftp/downloads/%s/status/"

	// Polling configuration for transfer status
	pollInterval       = 2 * time.Second
	basePollTimeout    = 30 * time.Second
	perFilePollTimeout = 10 * time.Second
	perMBPollTimeout   = 5 * time.Second

	bulkUploadConcurrency = 4 // uploads transfer payload bytes, keep low
	bulkPollConcurrency   = 8 // status polls are lightweight, allow more
)

// PollTransferStatus polls the transfer status API until success/failure or timeout.
// transferType should be "upload" or "download", id is the transfer ID.
// timeout controls how long to poll before giving up.
// Returns true if transfer succeeded, false if failed, and error if polling timed out or failed.
func PollTransferStatus(ac *client.AlpaconClient, transferType, id string, timeout time.Duration) (bool, string, error) {
	var statusURL string
	if transferType == "upload" {
		statusURL = fmt.Sprintf(uploadStatusURL, id)
	} else {
		statusURL = fmt.Sprintf(downloadStatusURL, id)
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

func uploadToS3(httpClient *http.Client, uploadURL string, file io.Reader, size int64) error {
	req, err := http.NewRequest(http.MethodPut, uploadURL, file)
	if err != nil {
		return err
	}
	req.ContentLength = size

	// Set GetBody so the body can be replayed on a redirect.
	if f, ok := osFileFrom(file); ok {
		name := f.Name()
		req.GetBody = func() (io.ReadCloser, error) {
			return os.Open(name)
		}
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

// osFileFrom unwraps readOnly to recover the underlying *os.File.
func osFileFrom(r io.Reader) (*os.File, bool) {
	switch v := r.(type) {
	case *os.File:
		return v, true
	case readOnly:
		f, ok := v.Reader.(*os.File)
		return f, ok
	}
	return nil, false
}

func uploadResponseLabel(resp UploadResponse) string {
	if resp.Name != "" {
		return resp.Name
	}
	return resp.ID
}

func collectConcurrentFailures(count, limit int, fn func(int) string) []string {
	if count == 0 {
		return nil
	}
	if limit < 1 {
		limit = 1
	}
	if limit > count {
		limit = count
	}

	failuresByIndex := make([]string, count)
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		sem <- struct{}{}
		wg.Add(1)
		go func(index int) {
			defer wg.Done()
			defer func() { <-sem }()
			failuresByIndex[index] = fn(index)
		}(i)
	}
	wg.Wait()

	var failures []string
	for _, failure := range failuresByIndex {
		if failure != "" {
			failures = append(failures, failure)
		}
	}
	return failures
}

func executeSingleUpload(ac *client.AlpaconClient, request *UploadRequest, file io.Reader, size int64) error {
	respBody, err := ac.SendPostRequest(uploadAPIURL, request)
	if err != nil {
		return err
	}

	var response UploadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	if response.UploadURL != "" {
		if err := uploadToS3(ac.HTTPClient, response.UploadURL, file, size); err != nil {
			return err
		}
	}

	triggerURL := utils.BuildURL(uploadAPIURL, fmt.Sprintf("%s/upload", response.ID), nil)
	if _, err := ac.SendGetRequest(triggerURL); err != nil {
		return err
	}

	timeout := calcPollTimeout(1, size)
	success, message, err := PollTransferStatus(ac, "upload", response.ID, timeout)
	if err != nil {
		return fmt.Errorf("upload transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

func executeBulkUpload(ac *client.AlpaconClient, request *BulkUploadRequest, files []io.Reader, sizes []int64) error {
	respBody, err := ac.SendPostRequest(uploadBulkAPIURL, request)
	if err != nil {
		return err
	}

	var responses []UploadResponse
	if err := json.Unmarshal(respBody, &responses); err != nil {
		return err
	}

	if len(responses) != len(files) {
		return fmt.Errorf("server returned %d upload slots but %d files were provided", len(responses), len(files))
	}

	ids := make([]string, len(responses))
	for i, resp := range responses {
		ids[i] = resp.ID
	}

	uploadFailures := collectConcurrentFailures(len(responses), bulkUploadConcurrency, func(i int) string {
		resp := responses[i]
		if resp.UploadURL == "" {
			return ""
		}
		if err := uploadToS3(ac.HTTPClient, resp.UploadURL, files[i], sizes[i]); err != nil {
			return fmt.Sprintf("%s: failed to upload to storage: %v", uploadResponseLabel(resp), err)
		}
		return ""
	})
	if len(uploadFailures) > 0 {
		return fmt.Errorf("upload failed for %d file(s):\n  %s", len(uploadFailures), strings.Join(uploadFailures, "\n  "))
	}

	// Trigger server-side processing
	triggerRequest := &BulkUploadTriggerRequest{IDs: ids}
	if _, err := ac.SendPostRequest(uploadBulkTriggerURL, triggerRequest); err != nil {
		return err
	}

	// Poll transfer status for each upload
	var totalBytes int64
	for _, s := range sizes {
		totalBytes += s
	}
	timeout := calcPollTimeout(len(files), totalBytes)

	failures := collectConcurrentFailures(len(responses), bulkPollConcurrency, func(i int) string {
		resp := responses[i]
		success, message, err := PollTransferStatus(ac, "upload", resp.ID, timeout)
		if err != nil {
			return fmt.Sprintf("%s: %v", uploadResponseLabel(resp), err)
		}
		if !success {
			return fmt.Sprintf("%s: %s", uploadResponseLabel(resp), message)
		}
		return ""
	})
	if len(failures) > 0 {
		return fmt.Errorf("upload failed for %d file(s):\n  %s", len(failures), strings.Join(failures, "\n  "))
	}

	return nil
}

// UploadFile uploads local files to a remote server.
// Uses the single upload API for one file, or the bulk API for multiple files.
// workSessionID is optional; when non-empty it is attached to the request body.
func UploadFile(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool, workSessionID string) error {
	serverName, remotePath := utils.SplitPath(dest)

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	if len(src) == 1 {
		f, err := os.Open(src[0])
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()

		stat, err := f.Stat()
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
			WorkSession:    workSessionID,
		}
		return executeSingleUpload(ac, request, readOnly{f}, stat.Size())
	}

	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	names := make([]string, len(src))
	files := make([]io.ReadCloser, len(src))
	sizes := make([]int64, len(src))
	for i, filePath := range src {
		f, err := os.Open(filePath)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = files[j].Close()
			}
			return err
		}
		stat, err := f.Stat()
		if err != nil {
			_ = f.Close()
			for j := 0; j < i; j++ {
				_ = files[j].Close()
			}
			return err
		}
		names[i] = filepath.Base(filePath)
		files[i] = f
		sizes[i] = stat.Size()
	}
	defer func() {
		for _, f := range files {
			if f != nil {
				_ = f.Close()
			}
		}
	}()

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
		WorkSession:    workSessionID,
	}

	readers := make([]io.Reader, len(files))
	for i, f := range files {
		readers[i] = readOnly{f}
	}
	return executeBulkUpload(ac, request, readers, sizes)
}

// UploadContent uploads an in-memory byte payload to remotePath on the named
// server, reusing the single-file upload pipeline. remotePath is the full
// destination path including the filename (cp semantics: the server writes to
// Path). workSessionID is optional; when non-empty it is attached to the
// request body.
func UploadContent(ac *client.AlpaconClient, serverName, remotePath string, content []byte, username, groupname string, allowOverwrite bool, workSessionID string) error {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	request := &UploadRequest{
		Name:           filepath.Base(remotePath),
		Path:           remotePath,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: allowOverwrite,
		WorkSession:    workSessionID,
	}

	return executeSingleUpload(ac, request, bytes.NewReader(content), int64(len(content)))
}

func createFolderZipTempFile(folderPath string) (*os.File, int64, error) {
	return utils.SpoolToTempFile("alpacon-folder-*.zip", func(w io.Writer) error {
		return utils.ZipToWriter(folderPath, w)
	})
}

// UploadFolder uploads local folders to a remote server.
// Each folder is zipped before upload and extracted on the server side.
// Uses the single upload API for one folder, or the bulk API for multiple folders.
// workSessionID is optional; when non-empty it is attached to the request body.
func UploadFolder(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool, workSessionID string) error {
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
		zipFile, size, err := createFolderZipTempFile(src[0])
		if err != nil {
			return err
		}
		defer utils.CleanupTempFile(zipFile)

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
			WorkSession:    workSessionID,
		}
		return executeSingleUpload(ac, request, readOnly{zipFile}, size)
	}

	names := make([]string, len(src))
	readers := make([]io.Reader, len(src))
	sizes := make([]int64, len(src))
	zipFiles := make([]*os.File, len(src))
	defer func() {
		for _, f := range zipFiles {
			utils.CleanupTempFile(f)
		}
	}()
	for i, folderPath := range src {
		zipFile, size, err := createFolderZipTempFile(folderPath)
		if err != nil {
			return err
		}
		zipFiles[i] = zipFile
		names[i] = filepath.Base(folderPath) + ".zip"
		readers[i] = readOnly{zipFile}
		sizes[i] = size
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
		WorkSession:    workSessionID,
	}

	return executeBulkUpload(ac, request, readers, sizes)
}

func fetchFromURLToFile(httpClient *http.Client, url, filePath string, maxAttempts int) (int64, error) {
	var resp *http.Response
	var err error

	for count := 0; count < maxAttempts; count++ {
		resp, err = httpClient.Get(url)
		if err != nil {
			return 0, fmt.Errorf("network error while downloading: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}
		_ = resp.Body.Close()

		// Client errors (4xx) will never succeed on retry
		if resp.StatusCode >= 400 && resp.StatusCode < 500 {
			return 0, fmt.Errorf("download failed with client error: %d", resp.StatusCode)
		}

		if count == maxAttempts-1 {
			return 0, fmt.Errorf("download failed after %d attempts (last status: %d)", maxAttempts, resp.StatusCode)
		}
		time.Sleep(time.Second)
	}

	defer func() { _ = resp.Body.Close() }()

	return utils.SaveStreamAtomic(filePath, resp.Body)
}

func downloadedFilePath(dest, remotePath string) (string, error) {
	// If dest is an existing directory or ends with a separator, append the remote filename.
	// Otherwise treat dest as the target file path directly (cp-style rename semantics).
	info, err := os.Stat(dest)
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("failed to access destination %q: %w", dest, err)
	}
	destHasTrailingSep := len(dest) > 0 && os.IsPathSeparator(dest[len(dest)-1])
	if (err == nil && info.IsDir()) || destHasTrailingSep {
		return filepath.Join(dest, filepath.Base(remotePath)), nil
	}

	return dest, nil
}

func reserveDownloadArchiveTempPath(dest string) (string, error) {
	if err := os.MkdirAll(dest, 0755); err != nil {
		return "", fmt.Errorf("failed to create destination directory: %w", err)
	}

	// Reserve a hidden path in dest so archive downloads never collide with
	// user-visible files returned by the server, such as "download.zip".
	f, err := os.CreateTemp(dest, ".alpacon-download-*.zip")
	if err != nil {
		return "", fmt.Errorf("failed to create temp archive: %w", err)
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return "", fmt.Errorf("failed to close temp archive: %w", err)
	}

	return name, nil
}

func saveDownloadedURL(httpClient *http.Client, url, dest, remotePath string, recursive bool, maxAttempts int) (int64, error) {
	if recursive {
		filePath, err := reserveDownloadArchiveTempPath(dest)
		if err != nil {
			return 0, err
		}
		defer func() { _ = utils.DeleteFile(filePath) }()

		written, err := fetchFromURLToFile(httpClient, url, filePath, maxAttempts)
		if err != nil {
			return written, err
		}
		if err := utils.Unzip(filePath, dest); err != nil {
			return written, fmt.Errorf("failed to extract downloaded folder: %w", err)
		}

		return written, nil
	}

	filePath, err := downloadedFilePath(dest, remotePath)
	if err != nil {
		return 0, err
	}

	return fetchFromURLToFile(httpClient, url, filePath, maxAttempts)
}

func downloadSingleFile(ac *client.AlpaconClient, remotePath, dest, serverID, username, groupname, resourceType, workSessionID string, recursive bool) error {
	downloadRequest := &DownloadRequest{
		Path:         remotePath,
		Name:         filepath.Base(remotePath),
		Server:       serverID,
		Username:     username,
		Groupname:    groupname,
		ResourceType: resourceType,
		WorkSession:  workSessionID,
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

	written, err := saveDownloadedURL(ac.HTTPClient, downloadResponse.DownloadURL, dest, remotePath, recursive, 100)
	if err != nil {
		return err
	}

	timeout := calcPollTimeout(1, written)
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
func downloadBulk(ac *client.AlpaconClient, remotePaths []string, dest, serverID, username, groupname, workSessionID string) error {
	spinner := utils.NewSpinner(fmt.Sprintf("Downloading %d files...", len(remotePaths)))
	spinner.Start()
	defer spinner.Stop()

	request := &BulkDownloadRequest{
		Path:        remotePaths,
		Server:      serverID,
		Username:    username,
		Groupname:   groupname,
		WorkSession: workSessionID,
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

	zipPath, err := reserveDownloadArchiveTempPath(dest)
	if err != nil {
		return err
	}
	defer func() { _ = utils.DeleteFile(zipPath) }()
	written, err := fetchFromURLToFile(ac.HTTPClient, response.DownloadURL, zipPath, 100)
	if err != nil {
		return fmt.Errorf("failed to save downloaded archive: %w", err)
	}

	if err := utils.Unzip(zipPath, dest); err != nil {
		return fmt.Errorf("failed to extract downloaded archive: %w", err)
	}

	timeout := calcPollTimeout(len(remotePaths), written)
	success, message, err := PollTransferStatus(ac, "download", response.ID, timeout)
	if err != nil {
		return fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

// DownloadFile downloads files from a remote server. Each source should be in
// "server:/path" format. Uses the bulk API for multiple files, or the
// single-file API for a single file.
// workSessionID is optional; when non-empty it is attached to the request body.
func DownloadFile(ac *client.AlpaconClient, sources []string, dest, username, groupname string, recursive bool, workSessionID string) error {
	if len(sources) == 0 {
		return fmt.Errorf("no source paths provided")
	}

	serverName, firstPath := utils.SplitPath(sources[0])

	// Extract remote paths and validate all sources are on the same server
	remotePaths := make([]string, 0, len(sources))
	remotePaths = append(remotePaths, strings.Trim(firstPath, "\""))
	for _, src := range sources[1:] {
		name, p := utils.SplitPath(src)
		if name != serverName {
			return fmt.Errorf("all sources must be on the same server (got %q and %q)", serverName, name)
		}
		remotePaths = append(remotePaths, strings.Trim(p, "\""))
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return err
	}

	if len(remotePaths) > 1 {
		return downloadBulk(ac, remotePaths, dest, serverID, username, groupname, workSessionID)
	}

	resourceType := "file"
	if recursive {
		resourceType = "folder"
	}

	return downloadSingleFile(ac, remotePaths[0], dest, serverID, username, groupname, resourceType, workSessionID, recursive)
}

// calcPollTimeout returns a dynamic poll timeout based on file count and total size.
// Base 30s + 10s per file + 5s per MB.
func calcPollTimeout(fileCount int, totalBytes int64) time.Duration {
	timeout := basePollTimeout +
		time.Duration(fileCount)*perFilePollTimeout +
		time.Duration(totalBytes/(1024*1024))*perMBPollTimeout
	return timeout
}
