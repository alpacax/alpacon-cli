package ftp

import (
	"bytes"
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

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/api/server"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

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
		// The URL is presigned: its query holds a live signature.
		return utils.WithRedirectStatus(utils.RedactURLError(err), resp)
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

func collectConcurrentFailures(count, limit int, fn func(int) error) []error {
	if count == 0 {
		return nil
	}
	limit = min(max(limit, 1), count)

	failuresByIndex := make([]error, count)
	sem := make(chan struct{}, limit)
	var wg sync.WaitGroup
	for i := range count {
		sem <- struct{}{}
		wg.Go(func() {
			defer func() { <-sem }()
			failuresByIndex[i] = fn(i)
		})
	}
	wg.Wait()

	var failures []error
	for _, failure := range failuresByIndex {
		if failure != nil {
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
	pathID, err := api.RequireResponseID(response.ID, "upload")
	if err != nil {
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

	triggerURL := api.IDURL(uploadAPIURL, pathID, "upload", nil)
	if err := transferStep(ac, serverID, func() error {
		_, err := ac.SendGetRequest(triggerURL)
		return err
	}); err != nil {
		return err
	}

	timeout := calcPollTimeout(1, size)
	success, message, err := pollTransfer(ac, serverID, uploadTransfer, pathID, timeout, time.Time{})
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
	pathIDs := make([]api.PathID, len(responses))
	for i := range responses {
		pathID, err := api.RequireResponseID(responses[i].ID, "upload")
		if err != nil {
			return err
		}
		pathIDs[i] = pathID
		responses[i].ID = pathID.String()
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

	uploadFailures := collectConcurrentFailures(len(responses), bulkUploadConcurrency, func(i int) error {
		resp := responses[i]
		if resp.UploadURL == "" {
			return nil
		}
		if err := uploadToS3(ac.HTTPClient, resp.UploadURL, files[i], sizes[i]); err != nil {
			return fmt.Errorf("%s: failed to upload to storage: %w", uploadResponseLabel(resp), err)
		}
		return nil
	})
	if len(uploadFailures) > 0 {
		return &utils.BatchError{Header: fmt.Sprintf("upload failed for %d file(s)", len(uploadFailures)), Failures: uploadFailures}
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
	failures := collectConcurrentFailures(len(responses), bulkPollConcurrency, func(i int) error {
		resp := responses[i]
		success, message, err := pollTransfer(ac, serverID, uploadTransfer, pathIDs[i], timeout, batchStart)
		if err != nil {
			return fmt.Errorf("%s: %w", uploadResponseLabel(resp), err)
		}
		if !success {
			return fmt.Errorf("%s: %s", uploadResponseLabel(resp), message)
		}
		return nil
	})
	if len(failures) > 0 {
		return &utils.BatchError{Header: fmt.Sprintf("upload failed for %d file(s)", len(failures)), Failures: failures}
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
	// A bad remote path is the caller's to fix whatever the local file holds.
	if _, _, err := splitRemoteFilePath(remotePath); err != nil {
		return err
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
	return uploadToExactPath(ac, serverName, remotePath, readOnly{f}, stat.Size(), username, groupname, workSessionID, true)
}

// UploadContentAs writes content to the exact remote file path as username and
// groupname. It sends the bytes it is given rather than reopening a local file,
// so a caller that hashes or submits the same bytes knows they are what landed
// on the server. Without allowOverwrite the agent refuses an existing file.
func UploadContentAs(ac *client.AlpaconClient, serverName, remotePath string, content []byte, username, groupname, workSessionID string, allowOverwrite bool) error {
	// A bytes.Reader lets net/http replay the body on a redirect.
	return uploadToExactPath(ac, serverName, remotePath, bytes.NewReader(content), int64(len(content)), username, groupname, workSessionID, allowOverwrite)
}

// uploadToExactPath uploads body to the exact remote file path. The server
// lookup and the upload create are tagged as the submission, so an MFA retry
// resends them and nothing after the create.
func uploadToExactPath(ac *client.AlpaconClient, serverName, remotePath string, body io.Reader, size int64, username, groupname, workSessionID string, allowOverwrite bool) error {
	remoteName, remoteDir, err := splitRemoteFilePath(remotePath)
	if err != nil {
		return err
	}

	serverID, err := server.GetServerIDByName(ac, serverName)
	if err != nil {
		return utils.MarkSubmission(err)
	}

	request := &UploadRequest{
		Name:           remoteName,
		Path:           remoteDir,
		Server:         serverID,
		Username:       username,
		Groupname:      groupname,
		AllowOverwrite: allowOverwrite,
		WorkSession:    workSessionID,
	}
	return executeSingleUpload(ac, request, body, size)
}

// splitRemoteFilePath splits an exact remote file path into the name and the
// directory an upload request carries, the directory ending in a slash.
func splitRemoteFilePath(remotePath string) (name, dir string, err error) {
	name, err = utils.RemoteFileName(remotePath)
	if err != nil {
		return "", "", err
	}
	dir = pathpkg.Dir(remotePath)
	if dir == "." {
		dir = ""
	} else if !strings.HasSuffix(dir, "/") {
		dir += "/"
	}
	return name, dir, nil
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
