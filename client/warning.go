package client

import (
	"net/http"
	"strings"
	"sync"

	"github.com/alpacax/alpacon-cli/utils"
)

// A command can create several clients; the warning belongs to the invocation.
var workSessionWarningOnce sync.Once

func warnWorkSessionRequiredSoon(header http.Header) {
	if !hasWarning(header, "work_session_required_soon") {
		return
	}
	sunset, err := http.ParseTime(header.Get("Sunset"))
	if err != nil {
		return
	}
	workSessionWarningOnce.Do(func() {
		utils.CliWarning("starting %s, this personal API token will need a Work Session for this action. Open a Work Session, or use a service token for automation.", sunset.UTC().Format("January 2, 2006"))
	})
}

// hasWarning matches one value in a repeated or comma-joined header.
func hasWarning(header http.Header, want string) bool {
	for _, line := range header.Values("Alpacon-Warning") {
		for value := range strings.SplitSeq(line, ",") {
			if strings.TrimSpace(value) == want {
				return true
			}
		}
	}
	return false
}
