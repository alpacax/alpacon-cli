package ftp

import (
	"time"
)

const (
	uploadAPIURL         = "/api/webftp/uploads/"
	uploadBulkAPIURL     = "/api/webftp/uploads/bulk/"
	uploadBulkTriggerURL = "/api/webftp/uploads/bulk-upload/"
	downloadAPIURL       = "/api/webftp/downloads/"
	downloadBulkAPIURL   = "/api/webftp/downloads/bulk/"

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
