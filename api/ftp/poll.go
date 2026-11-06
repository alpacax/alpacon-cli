package ftp

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// transferKind names an upload or a download and the route its transfers live under.
type transferKind struct{ name, apiURL string }

var (
	uploadTransfer   = transferKind{name: "upload", apiURL: uploadAPIURL}
	downloadTransfer = transferKind{name: "download", apiURL: downloadAPIURL}
)

// transferStatusURL is the status endpoint of an upload or download transfer.
func transferStatusURL(kind transferKind, id api.PathID) string {
	return api.IDURL(kind.apiURL, id, "status", nil)
}

func pollTransferStatus(ac *client.AlpaconClient, kind transferKind, id api.PathID, timeout, tick time.Duration) (bool, string, error) {
	statusURL := transferStatusURL(kind, id)

	start := time.Now()
	deadline := start.Add(timeout)
	budget := utils.NewThrottleBudget(timeout)
	failures := 0
	throttles := 0
	// lastThrottle is the 429 the latest poll got, if it got one, so a poll
	// that times out while throttled still carries the server's hint.
	var lastThrottle error

	// Checked before every request, since time.Sleep can oversleep the deadline.
	for time.Now().Before(deadline) {
		// A running transfer answers 200 with "success": null; only true or
		// false is terminal.
		respBody, err := ac.SendGetRequest(statusURL)
		var delay time.Duration
		lastThrottle = nil
		switch {
		case err != nil && !utils.IsTransientRequestError(err):
			return false, "", fmt.Errorf("failed to check transfer status: %w", err)
		case err != nil && utils.HTTPStatusCode(err) == http.StatusTooManyRequests:
			lastThrottle = err
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

	timedOut := fmt.Sprintf("transfer status polling timed out after %v", timeout)
	// Only the throttle's hint goes with the timeout, not its status or code.
	return false, "", utils.HintOnly(timedOut, lastThrottle)
}

// calcPollTimeout returns a dynamic poll timeout based on file count and total size.
// Base 30s + 10s per file + 5s per MB.
func calcPollTimeout(fileCount int, totalBytes int64) time.Duration {
	timeout := basePollTimeout +
		time.Duration(fileCount)*perFilePollTimeout +
		time.Duration(totalBytes/(1024*1024))*perMBPollTimeout
	return timeout
}
