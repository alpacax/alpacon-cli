package ftp

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	pathpkg "path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/mfa"
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

	backoffFactor = 2

	pollTick = 250 * time.Millisecond

	downloadMaxAttempts = 100

	// A server that answered "too many requests" is not persuaded by more of them,
	// and without Retry-After the window is a guess—so this errs small.
	throttledMaxAttempts = 10

	// The drain only buys connection reuse, and the shared client sets no timeout,
	// so this bound is the retry loop's only defense against a stalled error body.
	maxDrainedErrorBody = 8 << 10

	basePollTimeout    = 30 * time.Second
	perFilePollTimeout = 10 * time.Second
	perMBPollTimeout   = 5 * time.Second

	bulkUploadConcurrency = 4 // uploads transfer payload bytes, keep low
	bulkPollConcurrency   = 8 // status polls are lightweight, allow more
)

// A brief blip recovers sooner than the flat one-second retry did, while
// downloadMaxAttempts still spans roughly the same ~99s.
// var, not const, so tests can shorten them.
var (
	initialDownloadRetryDelay = 250 * time.Millisecond
	maxDownloadRetryDelay     = time.Second
)

// backoffDelay returns initial doubled once per 0-based attempt, capped at limit.
func backoffDelay(attempt int, initial, limit time.Duration) time.Duration {
	d := min(initial, limit)
	for range attempt {
		d *= backoffFactor
		if d >= limit {
			return limit
		}
	}
	return d
}

// PollTransferStatus polls the transfer status API until success/failure or timeout.
// transferType should be "upload" or "download", id is the transfer ID.
// timeout controls how long to poll before giving up.
// Returns true if transfer succeeded, false if failed, and error if polling timed out or failed.
func PollTransferStatus(ac *client.AlpaconClient, transferType, id string, timeout time.Duration) (bool, string, error) {
	return pollTransferStatus(ac, transferType, id, timeout, pollTick)
}

// transferStatusURL is the status endpoint of an upload or download transfer.
func transferStatusURL(transferType, id string) string {
	if transferType == "upload" {
		return fmt.Sprintf(uploadStatusURL, id)
	}
	return fmt.Sprintf(downloadStatusURL, id)
}

func pollTransferStatus(ac *client.AlpaconClient, transferType, id string, timeout, tick time.Duration) (bool, string, error) {
	statusURL := transferStatusURL(transferType, id)

	start := time.Now()
	deadline := start.Add(timeout)
	budget := utils.NewThrottleBudget(timeout)
	failures := 0
	throttles := 0

	// Checked before every request, since time.Sleep can oversleep the deadline.
	for time.Now().Before(deadline) {
		// A running transfer answers 200 with "success": null; only true or
		// false is terminal.
		respBody, err := ac.SendGetRequest(statusURL)
		var delay time.Duration
		switch {
		case err != nil && !utils.IsTransientRequestError(err):
			return false, "", fmt.Errorf("failed to check transfer status: %w", err)
		case err != nil && utils.HTTPStatusCode(err) == http.StatusTooManyRequests:
			delay = utils.NextPollBackoff(tick, throttles, utils.RetryAfter(err))
			throttles++
			budget.WarnThrottled(delay)
			if newDeadline, extended := budget.Extend(deadline, delay); extended {
				deadline = newDeadline
			}
		case err != nil:
			failures++
			if failures >= utils.MaxConsecutivePollFailures {
				return false, "", fmt.Errorf("failed to check transfer status: %w", err)
			}
			delay = utils.NextPollBackoff(tick, failures-1, utils.RetryAfter(err))
		default:
			failures, throttles = 0, 0
			var statusResp TransferStatusResponse
			if err := json.Unmarshal(respBody, &statusResp); err != nil {
				return false, "", fmt.Errorf("failed to parse transfer status response: %w", err)
			}
			if statusResp.Success != nil {
				return *statusResp.Success, statusResp.Message, nil
			}
			delay = utils.NextPollTick(tick, time.Since(start))
		}

		if !time.Now().Add(delay).Before(deadline) {
			break
		}
		time.Sleep(delay)
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
	for i := range count {
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
	return executeSingleUploadFrom(ac, request, func() (io.Reader, int64, func(), error) {
		return file, size, func() {}, nil
	})
}

// uploadSource produces what an upload sends once the server has accepted the
// create request, so a create the server refuses (an MFA step-up the caller
// retries, say) costs no preparation. cleanup runs when the upload ends.
type uploadSource func() (file io.Reader, size int64, cleanup func(), err error)

// bulkUploadSource is uploadSource for several files.
type bulkUploadSource func() (files []io.Reader, sizes []int64, cleanup func(), err error)

func executeSingleUploadFrom(ac *client.AlpaconClient, request *UploadRequest, open uploadSource) error {
	respBody, err := ac.SendPostRequest(uploadAPIURL, request)
	if err != nil {
		return utils.MarkSubmission(err)
	}
	// The server accepted the upload; what fails after it is returned untagged,
	// so it never uploads again.
	return completeSingleUpload(ac, request.Server, respBody, open)
}

// completeSingleUpload sends the file to the slot the server created, starts
// the transfer and waits for it. Each request after the create is its own retry
// unit (transferStep): an MFA refusal of one is retried alone.
func completeSingleUpload(ac *client.AlpaconClient, serverID string, respBody []byte, open uploadSource) error {
	var response UploadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	file, size, cleanup, err := open()
	if err != nil {
		return err
	}
	defer cleanup()

	if response.UploadURL != "" {
		if err := uploadToS3(ac.HTTPClient, response.UploadURL, file, size); err != nil {
			return err
		}
	}

	triggerURL := utils.BuildURL(uploadAPIURL, fmt.Sprintf("%s/upload", response.ID), nil)
	if err := transferStep(ac, serverID, func() error {
		_, err := ac.SendGetRequest(triggerURL)
		return err
	}); err != nil {
		return err
	}

	timeout := calcPollTimeout(1, size)
	success, message, err := pollTransfer(ac, serverID, "upload", response.ID, timeout, time.Time{})
	if err != nil {
		return fmt.Errorf("upload transfer status check failed: %w", err)
	}
	if !success {
		return fmt.Errorf("%s", message)
	}

	return nil
}

func executeBulkUpload(ac *client.AlpaconClient, request *BulkUploadRequest, files []io.Reader, sizes []int64) error {
	return executeBulkUploadFrom(ac, request, func() ([]io.Reader, []int64, func(), error) {
		return files, sizes, func() {}, nil
	})
}

func executeBulkUploadFrom(ac *client.AlpaconClient, request *BulkUploadRequest, open bulkUploadSource) error {
	respBody, err := ac.SendPostRequest(uploadBulkAPIURL, request)
	if err != nil {
		return utils.MarkSubmission(err)
	}
	// The server accepted the uploads; what fails after it is returned untagged,
	// so it never uploads again.
	return completeBulkUpload(ac, request.Server, respBody, open)
}

// completeBulkUpload sends each file to the slot the server created for it,
// starts the transfers and waits for them. Each request after the create is its
// own retry unit (transferStep).
func completeBulkUpload(ac *client.AlpaconClient, serverID string, respBody []byte, open bulkUploadSource) error {
	var responses []UploadResponse
	if err := json.Unmarshal(respBody, &responses); err != nil {
		return err
	}

	files, sizes, cleanup, err := open()
	if err != nil {
		return err
	}
	defer cleanup()

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
	if err := transferStep(ac, serverID, func() error {
		_, err := ac.SendPostRequest(uploadBulkTriggerURL, triggerRequest)
		return err
	}); err != nil {
		return err
	}

	// Poll transfer status for each upload
	var totalBytes int64
	for _, s := range sizes {
		totalBytes += s
	}
	timeout := calcPollTimeout(len(files), totalBytes)

	// One start time for the whole batch: the polls queue behind the concurrency
	// limit, and all of them belong to this command's MFA wait.
	batchStart := time.Now()
	failures := collectConcurrentFailures(len(responses), bulkPollConcurrency, func(i int) string {
		resp := responses[i]
		success, message, err := pollTransfer(ac, serverID, "upload", resp.ID, timeout, batchStart)
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
	serverName, remotePath, err := utils.SplitPath(dest)
	if err != nil {
		return err
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
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
			for j := range i {
				_ = files[j].Close()
			}
			return err
		}
		stat, err := f.Stat()
		if err != nil {
			_ = f.Close()
			for j := range i {
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

// UploadLocalFileAs uploads one local file to the exact remote file path.
// It preserves the remote basename instead of deriving the destination name
// from the local temp file name.
func UploadLocalFileAs(ac *client.AlpaconClient, localPath, serverName, remotePath, username, groupname, workSessionID string) error {
	remoteName, err := utils.RemoteFileName(remotePath)
	if err != nil {
		return err
	}
	remoteDir := pathpkg.Dir(remotePath)
	if remoteDir == "." {
		remoteDir = ""
	} else if !strings.HasSuffix(remoteDir, "/") {
		remoteDir += "/"
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
	}

	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	stat, err := f.Stat()
	if err != nil {
		return err
	}

	request := &UploadRequest{
		Name:           remoteName,
		Path:           remoteDir,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: true,
		WorkSession:    workSessionID,
	}
	return executeSingleUpload(ac, request, readOnly{f}, stat.Size())
}

func createFolderZipTempFile(folderPath string) (*os.File, int64, error) {
	return utils.SpoolToTempFile("alpacon-folder-*.zip", func(w io.Writer) error {
		return utils.ZipToWriter(folderPath, w)
	})
}

// checkFolderReadable fails for a path that does not exist or whose entries
// cannot be listed.
func checkFolderReadable(folderPath string) error {
	dir, err := os.Open(folderPath)
	if err != nil {
		return err
	}
	defer func() { _ = dir.Close() }()
	if _, err := dir.Readdirnames(1); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// UploadFolder uploads local folders to a remote server.
// Each folder is zipped before upload and extracted on the server side.
// Uses the single upload API for one folder, or the bulk API for multiple folders.
// workSessionID is optional; when non-empty it is attached to the request body.
func UploadFolder(ac *client.AlpaconClient, src []string, dest, username, groupname string, allowOverwrite bool, workSessionID string) error {
	serverName, remotePath, err := utils.SplitPath(dest)
	if err != nil {
		return err
	}

	// Folder uploads always target a directory; ensure trailing slash so the
	// server recognises the path as a directory.
	if !strings.HasSuffix(remotePath, "/") {
		remotePath += "/"
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
	}

	// A folder that is missing or cannot be listed fails here, before the server
	// is asked for an upload slot it would then leave orphaned.
	for _, folderPath := range src {
		if err := checkFolderReadable(folderPath); err != nil {
			return err
		}
	}

	// The zip is built only after the server accepts the create request: an
	// attempt it refuses, such as one an MFA step-up retries, zips nothing.
	if len(src) == 1 {
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
		return executeSingleUploadFrom(ac, request, func() (io.Reader, int64, func(), error) {
			zipFile, size, err := createFolderZipTempFile(src[0])
			if err != nil {
				return nil, 0, nil, err
			}
			return readOnly{zipFile}, size, func() { utils.CleanupTempFile(zipFile) }, nil
		})
	}

	names := make([]string, len(src))
	for i, folderPath := range src {
		names[i] = filepath.Base(folderPath) + ".zip"
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

	return executeBulkUploadFrom(ac, request, func() ([]io.Reader, []int64, func(), error) {
		readers := make([]io.Reader, len(src))
		sizes := make([]int64, len(src))
		zipFiles := make([]*os.File, len(src))
		cleanup := func() {
			for _, f := range zipFiles {
				utils.CleanupTempFile(f)
			}
		}
		for i, folderPath := range src {
			zipFile, size, err := createFolderZipTempFile(folderPath)
			if err != nil {
				cleanup()
				return nil, nil, nil, err
			}
			zipFiles[i] = zipFile
			readers[i] = readOnly{zipFile}
			sizes[i] = size
		}
		return readers, sizes, cleanup, nil
	})
}

func fetchFromURLToFile(httpClient *http.Client, url, filePath string, maxAttempts int) (int64, error) {
	var resp *http.Response
	var err error

	for count := range maxAttempts {
		resp, err = httpClient.Get(url)
		if err != nil {
			return 0, fmt.Errorf("network error while downloading: %w", err)
		}

		if resp.StatusCode == http.StatusOK {
			break
		}
		// An unread body keeps net/http from reusing the connection, so every retry
		// would open a new one. A refusal's body names why, which is how an MFA
		// refusal is told from any other 4xx.
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxDrainedErrorBody))
		_ = resp.Body.Close()
		var refusalCode, refusalSource string
		if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
			refusalCode, refusalSource = parseRefusal(body)
		}

		if utils.IsFatalClientError(resp.StatusCode) {
			return 0, fetchStatusError{status: resp.StatusCode, code: refusalCode, source: refusalSource}
		}

		budget := maxAttempts
		if utils.IsRetryLaterStatus(resp.StatusCode) {
			budget = min(budget, throttledMaxAttempts)
		}
		if count >= budget-1 {
			return 0, fmt.Errorf("download failed after %d attempts (last status: %d)", count+1, resp.StatusCode)
		}
		time.Sleep(backoffDelay(count, initialDownloadRetryDelay, maxDownloadRetryDelay))
	}

	defer func() { _ = resp.Body.Close() }()

	// Remote files routinely carry secrets, so a new download lands owner-only.
	return utils.SaveStreamAtomic(filePath, resp.Body, 0600)
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

// saveDownloadedURL writes the downloaded content and returns the resolved local path.
func saveDownloadedURL(httpClient *http.Client, url, dest, remotePath string, recursive bool, maxAttempts int) (string, int64, error) {
	if recursive {
		filePath, err := reserveDownloadArchiveTempPath(dest)
		if err != nil {
			return "", 0, err
		}
		defer func() { _ = utils.DeleteFile(filePath) }()

		written, err := fetchFromURLToFile(httpClient, url, filePath, maxAttempts)
		if err != nil {
			return dest, written, err
		}
		if err := utils.Unzip(filePath, dest); err != nil {
			return dest, written, fmt.Errorf("failed to extract downloaded folder: %w", err)
		}

		return dest, written, nil
	}

	filePath, err := downloadedFilePath(dest, remotePath)
	if err != nil {
		return "", 0, err
	}

	written, err := fetchFromURLToFile(httpClient, url, filePath, maxAttempts)
	return filePath, written, err
}

func downloadSingleFileWithResult(ac *client.AlpaconClient, remotePath, dest, serverID, username, groupname, resourceType, workSessionID string, recursive bool) (DownloadedFile, error) {
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
		return DownloadedFile{}, utils.MarkSubmission(err)
	}
	// The server accepted the download and runs it; what fails after it is
	// returned untagged, so it never starts another.
	return completeSingleDownload(ac, serverID, postBody, remotePath, dest, recursive)
}

// completeSingleDownload waits for the download the server started, fetches
// the file and confirms the transfer. Each request after the create is its own
// retry unit (transferStep).
func completeSingleDownload(ac *client.AlpaconClient, serverID string, postBody []byte, remotePath, dest string, recursive bool) (DownloadedFile, error) {
	var downloadResponse DownloadResponse
	if err := json.Unmarshal(postBody, &downloadResponse); err != nil {
		return DownloadedFile{}, err
	}

	var status event.EventDetails
	if err := transferStep(ac, serverID, func() (err error) {
		status, err = event.PollCommandExecution(ac, downloadResponse.Command)
		return err
	}); err != nil {
		return DownloadedFile{}, err
	}

	if status.Status == "stuck" || status.Status == "error" {
		return DownloadedFile{}, fmt.Errorf("command failed with status: %s", status.Status)
	}
	if status.Status == "failed" {
		return DownloadedFile{}, fmt.Errorf("%s", status.Result)
	}

	var (
		localPath string
		written   int64
	)
	if err := transferStep(ac, serverID, func() (err error) {
		localPath, written, err = saveDownloadedURL(ac.HTTPClient, downloadResponse.DownloadURL, dest, remotePath, recursive, downloadMaxAttempts)
		return err
	}); err != nil {
		return DownloadedFile{}, err
	}

	timeout := calcPollTimeout(1, written)
	success, message, err := pollTransfer(ac, serverID, "download", downloadResponse.ID, timeout, time.Time{})
	if err != nil {
		return DownloadedFile{}, fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return DownloadedFile{}, fmt.Errorf("%s", message)
	}

	// Report the bytes actually written to disk; the edit size guard must reflect
	// the local file, not a possibly stale or incorrect server-reported size.
	return DownloadedFile{Path: localPath, Size: written}, nil
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
		return utils.MarkSubmission(err)
	}
	// The server accepted the download and runs it; what fails after it is
	// returned untagged, so it never starts another.
	return completeBulkDownload(ac, serverID, respBody, remotePaths, dest)
}

// completeBulkDownload waits for the archive the server builds, fetches and
// extracts it, and confirms the transfer. Each request after the create is its
// own retry unit (transferStep).
func completeBulkDownload(ac *client.AlpaconClient, serverID string, respBody []byte, remotePaths []string, dest string) error {
	var response BulkDownloadResponse
	if err := json.Unmarshal(respBody, &response); err != nil {
		return err
	}

	var status event.EventDetails
	if err := transferStep(ac, serverID, func() (err error) {
		status, err = event.PollCommandExecution(ac, response.Command)
		return err
	}); err != nil {
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
	var written int64
	if err := transferStep(ac, serverID, func() (err error) {
		written, err = fetchFromURLToFile(ac.HTTPClient, response.DownloadURL, zipPath, downloadMaxAttempts)
		return err
	}); err != nil {
		return fmt.Errorf("failed to save downloaded archive: %w", err)
	}

	if err := utils.Unzip(zipPath, dest); err != nil {
		return fmt.Errorf("failed to extract downloaded archive: %w", err)
	}

	timeout := calcPollTimeout(len(remotePaths), written)
	success, message, err := pollTransfer(ac, serverID, "download", response.ID, timeout, time.Time{})
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

	serverName, firstPath, err := utils.SplitPath(sources[0])
	if err != nil {
		return err
	}

	// Extract remote paths and validate all sources are on the same server
	remotePaths := make([]string, 0, len(sources))
	remotePaths = append(remotePaths, strings.Trim(firstPath, "\""))
	for _, src := range sources[1:] {
		name, p, err := utils.SplitPath(src)
		if err != nil {
			return err
		}
		if name != serverName {
			return fmt.Errorf("all sources must be on the same server (got %q and %q)", serverName, name)
		}
		remotePaths = append(remotePaths, strings.Trim(p, "\""))
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
	}

	if len(remotePaths) > 1 {
		return downloadBulk(ac, remotePaths, dest, serverID, username, groupname, workSessionID)
	}

	resourceType := "file"
	if recursive {
		resourceType = "folder"
	}

	_, err = downloadSingleFileWithResult(ac, remotePaths[0], dest, serverID, username, groupname, resourceType, workSessionID, recursive)
	return err
}

func DownloadFileToPath(ac *client.AlpaconClient, serverName, remotePath, localPath, username, groupname, workSessionID string) (DownloadedFile, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return DownloadedFile{}, utils.MarkSubmission(err)
	}
	return downloadSingleFileWithResult(ac, remotePath, localPath, serverID, username, groupname, "file", workSessionID, false)
}

// calcPollTimeout returns a dynamic poll timeout based on file count and total size.
// Base 30s + 10s per file + 5s per MB.
func calcPollTimeout(fileCount int, totalBytes int64) time.Duration {
	timeout := basePollTimeout +
		time.Duration(fileCount)*perFilePollTimeout +
		time.Duration(totalBytes/(1024*1024))*perMBPollTimeout
	return timeout
}

// fetchStatusError is the refusal a download URL answered with. It carries the
// code the response body named, so an MFA refusal reads as one.
type fetchStatusError struct {
	status       int
	code, source string
}

func (e fetchStatusError) Error() string {
	return fmt.Sprintf("download failed with client error: %d", e.status)
}
func (e fetchStatusError) HTTPStatusCode() int { return e.status }
func (e fetchStatusError) ErrorCode() string   { return e.code }
func (e fetchStatusError) ErrorSource() string { return e.source }

func parseRefusal(body []byte) (code, source string) {
	var refusal utils.ErrorResponse
	if json.Unmarshal(body, &refusal) != nil {
		return "", ""
	}
	return refusal.Code, refusal.Source
}

// mfaGate lets the requests of transfers on one client and server share one MFA
// wait: the bulk status polls run concurrently and are refused together. The
// lock is held only for the wait itself. endedAt and endErr record when the
// last wait ended and how, so a request refused before that moment takes its
// outcome instead of starting another wait. Transfers of another client or
// server have their own gate.
type mfaGate struct {
	mu      sync.Mutex
	endedAt time.Time
	endErr  error
}

type mfaGateKey struct {
	ac       *client.AlpaconClient
	serverID string
}

var mfaGates sync.Map // mfaGateKey -> *mfaGate

func gateFor(ac *client.AlpaconClient, serverID string) *mfaGate {
	gate, _ := mfaGates.LoadOrStore(mfaGateKey{ac: ac, serverID: serverID}, &mfaGate{})
	return gate.(*mfaGate)
}

// await runs wait unless one ended after refusedAt, in which case it returns
// that wait's outcome. waited reports whether this call ran the wait.
func (g *mfaGate) await(refusedAt time.Time, wait func() error) (waited bool, err error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.endedAt.After(refusedAt) {
		return false, g.endErr
	}
	err = wait()
	g.endedAt, g.endErr = time.Now(), err
	return true, err
}

// transferStep sends one request of a transfer the server already created, as
// its own retry unit. If MFA freshness lapsed and the server refuses it for
// MFA, the user is prompted and only that request is sent again once MFA
// completes; the transfer is never created a second time. Any other result is
// returned as is, untagged: a request after the create is never a submission
// that may be resent. Refusals that arrive together share one MFA wait, and
// when it fails each of them returns its error without prompting again.
func transferStep(ac *client.AlpaconClient, serverID string, step func() error) error {
	return transferStepProbed(ac, serverID, time.Time{}, step, nil)
}

// transferStepProbed is transferStep for a step that is a whole loop of
// requests, a status poll: the MFA wait retries probe, one request, and the
// step runs again in full only after the wait, outside the gate, so the
// refused steps of sibling transfers proceed in parallel. A nil probe is the
// step itself.
//
// A refusal that began before the last wait ended takes that wait's outcome. For
// a step of its own that moment is when the step started (a zero refusedAt). The
// polls of one bulk command are queued behind a concurrency limit, so some start
// only after a wait ended; the command passes the time it began so all of them
// count as refused before it. A refusal from a separate, later command starts
// after and still prompts.
func transferStepProbed(ac *client.AlpaconClient, serverID string, refusedAt time.Time, step, probe func() error) error {
	if refusedAt.IsZero() {
		refusedAt = time.Now()
	}
	err := step()
	if utils.StructuredErrorCode(err) != utils.AuthMFARequired {
		return err
	}

	retry := probe
	if retry == nil {
		retry = step
	}
	waited, werr := gateFor(ac, serverID).await(refusedAt, func() error {
		return utils.HandleCommonErrors(utils.MarkSubmission(err), "", utils.ErrorHandlerCallbacks{
			OnMFARequired: func(string) error { return mfa.HandleMFAErrorForServerID(ac, serverID) },
			RetryOperation: func() error {
				return utils.MarkSubmission(retry())
			},
		})
	})
	if werr != nil {
		return werr
	}
	if waited && probe == nil {
		// The retried request was the step itself, and it went through.
		return nil
	}
	return step()
}

// pollTransfer is PollTransferStatus with the poll as a retry unit. refusedAt is
// the time the command began, for polls queued behind a concurrency limit; the
// zero time means the poll's own start (see transferStepProbed). A poll
// refused for MFA cannot say how the transfer ended, so when it is refused and
// the wait does not end in an answer, the error says the transfer may already
// have completed.
func pollTransfer(ac *client.AlpaconClient, serverID, transferType, id string, timeout time.Duration, refusedAt time.Time) (success bool, message string, err error) {
	refused := false
	note := func(stepErr error) error {
		if utils.StructuredErrorCode(stepErr) == utils.AuthMFARequired {
			refused = true
		}
		return stepErr
	}
	err = transferStepProbed(ac, serverID, refusedAt,
		func() error {
			var stepErr error
			success, message, stepErr = PollTransferStatus(ac, transferType, id, timeout)
			return note(stepErr)
		},
		func() error {
			// One status read: it tells whether MFA went through without
			// running the whole poll under the gate. A transient failure is
			// ridden through as the full poll rides through it, so a flaky read
			// does not end the wait.
			var probeErr error
			for failures := range utils.MaxConsecutivePollFailures {
				_, probeErr = ac.SendGetRequest(transferStatusURL(transferType, id))
				if probeErr == nil || !utils.IsTransientRequestError(probeErr) {
					break
				}
				time.Sleep(utils.NextPollBackoff(pollTick, failures, utils.RetryAfter(probeErr)))
			}
			return note(probeErr)
		})
	if err != nil && refused {
		err = fmt.Errorf("%w (the %s may already have completed on the server; check before running it again)", err, transferType)
	}
	return success, message, err
}
