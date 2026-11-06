package ftp

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/event"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

func fetchFromURLToFile(httpClient *http.Client, url, filePath string, maxAttempts int) (int64, error) {
	resp, err := fetchFromURL(httpClient, url, maxAttempts)
	if err != nil {
		return 0, err
	}
	defer func() { _ = resp.Body.Close() }()

	// Remote files routinely carry secrets, so a new download lands owner-only.
	return utils.SaveStreamAtomic(filePath, resp.Body, 0600)
}

// fetchFromURLLimited reads at most limit bytes of the file at url into
// memory, so a file far larger than the caller accepts is never read whole.
func fetchFromURLLimited(httpClient *http.Client, url string, limit int64, maxAttempts int) ([]byte, error) {
	resp, err := fetchFromURL(httpClient, url, maxAttempts)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()

	data, err := io.ReadAll(io.LimitReader(resp.Body, limit))
	if err != nil {
		return nil, fmt.Errorf("network error while downloading: %w", utils.RedactURLError(err))
	}
	return data, nil
}

// fetchFromURL GETs url, retrying a retryable status up to maxAttempts times,
// and returns the 200 response; the caller closes its body.
func fetchFromURL(httpClient *http.Client, url string, maxAttempts int) (*http.Response, error) {
	var resp *http.Response
	var err error

	for count := range maxAttempts {
		resp, err = httpClient.Get(url)
		if err != nil {
			return nil, fmt.Errorf("network error while downloading: %w", utils.WithRedirectStatus(utils.RedactURLError(err), resp))
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
			return nil, fetchStatusError{
				status: resp.StatusCode, code: refusalCode, source: refusalSource,
				hint: utils.ParseRefusalHint(body, resp.StatusCode),
			}
		}

		budget := maxAttempts
		if utils.IsRetryLaterStatus(resp.StatusCode) {
			budget = min(budget, throttledMaxAttempts)
		}
		if count >= budget-1 {
			// A refusal that outlasted the retries, a 429 in practice, keeps its
			// code and the server's hint, so the error output can print the hint.
			if utils.IsRefusalStatus(resp.StatusCode) {
				code, source := parseRefusal(body)
				return nil, fetchStatusError{
					status: resp.StatusCode, code: code, source: source,
					hint: utils.ParseRefusalHint(body, resp.StatusCode), attempts: count + 1,
				}
			}
			return nil, fmt.Errorf("download failed after %d attempts (last status: %d)", count+1, resp.StatusCode)
		}
		time.Sleep(utils.CappedBackoff(initialDownloadRetryDelay, count, maxDownloadRetryDelay))
	}
	return resp, nil
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
	spinner := utils.NewSpinner(fmt.Sprintf("Downloading %s...", filepath.Base(remotePath)))
	spinner.Start()
	defer spinner.Stop()

	postBody, err := createSingleDownload(ac, remotePath, serverID, username, groupname, resourceType, workSessionID)
	if err != nil {
		return DownloadedFile{}, err
	}
	// The server accepted the download and runs it; what fails after it is
	// returned untagged, so it never starts another.
	return completeSingleDownload(ac, serverID, postBody, remotePath, dest, recursive)
}

// createSingleDownload asks the server to start downloading one remote path.
// Its refusal is tagged as the submission, the one request a retry may resend.
func createSingleDownload(ac *client.AlpaconClient, remotePath, serverID, username, groupname, resourceType, workSessionID string) ([]byte, error) {
	downloadRequest := &DownloadRequest{
		Path:         remotePath,
		Name:         filepath.Base(remotePath),
		Server:       serverID,
		Username:     username,
		Groupname:    groupname,
		ResourceType: resourceType,
		WorkSession:  workSessionID,
	}
	postBody, err := ac.SendPostRequest(downloadAPIURL, downloadRequest)
	if err != nil {
		return nil, utils.MarkSubmission(err)
	}
	return postBody, nil
}

// downloadIDs checks the transfer and command IDs a download create returned,
// before any of them reaches a path.
func downloadIDs(transferID, commandID string) (api.PathID, api.PathID, error) {
	transferPathID, err := api.RequireResponseID(transferID, "download")
	if err != nil {
		return api.PathID{}, api.PathID{}, err
	}
	commandPathID, err := api.RequireResponseID(commandID, "command")
	if err != nil {
		return api.PathID{}, api.PathID{}, err
	}
	return transferPathID, commandPathID, nil
}

// completeSingleDownload waits for the download the server started, fetches
// the file to dest and confirms the transfer.
func completeSingleDownload(ac *client.AlpaconClient, serverID string, postBody []byte, remotePath, dest string, recursive bool) (DownloadedFile, error) {
	var localPath string
	written, err := completeSingleDownloadWith(ac, serverID, postBody, func(url string) (written int64, err error) {
		localPath, written, err = saveDownloadedURL(ac.HTTPClient, url, dest, remotePath, recursive, downloadMaxAttempts)
		return written, err
	})
	if err != nil {
		return DownloadedFile{}, err
	}
	// Report the bytes actually written to disk; the edit size guard must reflect
	// the local file, not a possibly stale or incorrect server-reported size.
	return DownloadedFile{Path: localPath, Size: written}, nil
}

// completeSingleDownloadWith waits for the download the server started, hands
// its URL to fetch and confirms the transfer, returning the bytes fetch read.
// Each request after the create is its own retry unit (transferStep).
func completeSingleDownloadWith(ac *client.AlpaconClient, serverID string, postBody []byte, fetch func(url string) (int64, error)) (int64, error) {
	var downloadResponse DownloadResponse
	if err := json.Unmarshal(postBody, &downloadResponse); err != nil {
		return 0, err
	}
	transferID, commandID, err := downloadIDs(downloadResponse.ID, downloadResponse.Command)
	if err != nil {
		return 0, err
	}

	var status event.EventDetails
	if err := transferStep(ac, serverID, func() (err error) {
		status, err = event.PollCommandExecution(ac, commandID)
		return err
	}); err != nil {
		return 0, err
	}

	if status.Status == "stuck" || status.Status == "error" {
		return 0, fmt.Errorf("command failed with status: %s", status.Status)
	}
	if status.Status == "failed" {
		return 0, fmt.Errorf("%s", status.Result)
	}

	var written int64
	if err := transferStep(ac, serverID, func() (err error) {
		written, err = fetch(downloadResponse.DownloadURL)
		return err
	}); err != nil {
		return 0, err
	}

	timeout := calcPollTimeout(1, written)
	success, message, err := pollTransfer(ac, serverID, downloadTransfer, transferID, timeout, time.Time{})
	if err != nil {
		return 0, fmt.Errorf("download transfer status check failed: %w", err)
	}
	if !success {
		return 0, fmt.Errorf("%s", message)
	}
	return written, nil
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
	transferID, commandID, err := downloadIDs(response.ID, response.Command)
	if err != nil {
		return err
	}

	var status event.EventDetails
	if err := transferStep(ac, serverID, func() (err error) {
		status, err = event.PollCommandExecution(ac, commandID)
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
	success, message, err := pollTransfer(ac, serverID, downloadTransfer, transferID, timeout, time.Time{})
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

// DownloadFileContent reads one remote file into memory, at most limit bytes of
// it: a caller that refuses anything larger learns so from limit+1 without the
// whole file crossing the network or touching the disk.
func DownloadFileContent(ac *client.AlpaconClient, serverName, remotePath, username, groupname, workSessionID string, limit int64) ([]byte, error) {
	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return nil, utils.MarkSubmission(err)
	}

	spinner := utils.NewSpinner(fmt.Sprintf("Downloading %s...", filepath.Base(remotePath)))
	spinner.Start()
	defer spinner.Stop()

	postBody, err := createSingleDownload(ac, remotePath, serverID, username, groupname, "file", workSessionID)
	if err != nil {
		return nil, err
	}
	// The server accepted the download and runs it; what fails after it is
	// returned untagged, so it never starts another.
	var content []byte
	_, err = completeSingleDownloadWith(ac, serverID, postBody, func(url string) (int64, error) {
		data, err := fetchFromURLLimited(ac.HTTPClient, url, limit, downloadMaxAttempts)
		content = data
		return int64(len(data)), err
	})
	if err != nil {
		return nil, err
	}
	return content, nil
}

// fetchStatusError is the refusal a download URL answered with. It carries the
// code the response body named, so an MFA refusal reads as one, and the
// server's hint, sanitized, which the error output prints under the error.
// attempts is set when the refusal is the last of a retried status that ran
// out of attempts.
type fetchStatusError struct {
	status       int
	code, source string
	hint         string
	attempts     int
}

func (e fetchStatusError) Error() string {
	if e.attempts > 0 {
		return fmt.Sprintf("download failed after %d attempts (last status: %d)", e.attempts, e.status)
	}
	return fmt.Sprintf("download failed with client error: %d", e.status)
}
func (e fetchStatusError) HTTPStatusCode() int { return e.status }
func (e fetchStatusError) ErrorCode() string   { return e.code }
func (e fetchStatusError) ErrorSource() string { return e.source }
func (e fetchStatusError) ErrorHint() string   { return e.hint }

func parseRefusal(body []byte) (code, source string) {
	var refusal utils.ErrorResponse
	if json.Unmarshal(body, &refusal) != nil {
		return "", ""
	}
	return refusal.Code, refusal.Source
}
