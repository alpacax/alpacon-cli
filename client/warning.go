package client

import (
	"net/http"
	"sync"

	"github.com/alpacax/alpacon-cli/utils"
)

// A command can create several clients; the warning belongs to the invocation.
var workSessionWarningOnce sync.Once

func warnWorkSessionRequiredSoon(header http.Header) {
	if header.Get("Alpacon-Warning") != "work_session_required_soon" {
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
