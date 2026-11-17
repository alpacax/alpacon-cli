package worksession

import (
	"testing"

	"github.com/alpacax/alpacon-cli/pkg/cmdtest"
	"github.com/alpacax/alpacon-cli/utils"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWorkSessionConstructorsKeepFlagValuesIndependent(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		newCmd func(clientFactory, *utils.IOStreams) *cobra.Command
		flag   string
		value  string
		want   string
	}{
		{"create purpose", newWorkSessionCreateCmd, "purpose", "incident", "incident"},
		{"create scopes", newWorkSessionCreateCmd, "scope", "command,sudo", "[command,sudo]"},
		{"create servers", newWorkSessionCreateCmd, "server", "web-1,web-2", "[web-1,web-2]"},
		{"create cluster", newWorkSessionCreateCmd, "cluster", "prod-east", "prod-east"},
		{"create expiry duration", newWorkSessionCreateCmd, "expires-in", "2h", "2h"},
		{"create expiry time", newWorkSessionCreateCmd, "expires-at", "2026-12-01T12:00:00Z", "2026-12-01T12:00:00Z"},
		{"create requester", newWorkSessionCreateCmd, "requester-type", "agent", "agent"},
		{"create wait", newWorkSessionCreateCmd, "wait", "true", "true"},
		{"create approval wait", newWorkSessionCreateCmd, "wait-approval", "10m", "10m"},
		{"create use", newWorkSessionCreateCmd, "use", "true", "true"},
		{"create sudo", newWorkSessionCreateCmd, "sudo", "id,whoami", `["id,whoami"]`},
		{"create sudo reason", newWorkSessionCreateCmd, "sudo-reason", "incident", "incident"},
		{"create account", newWorkSessionCreateCmd, "account", "root", "root"},
		{"extend expiry duration", newWorkSessionExtendCmd, "expires-in", "2h", "2h"},
		{"extend expiry time", newWorkSessionExtendCmd, "expires-at", "2026-12-01T12:00:00Z", "2026-12-01T12:00:00Z"},
		{"extend reason", newWorkSessionExtendCmd, "reason", "incident", "incident"},
		{"recording terminal", newWorkSessionRecordingCmd, "index", "2", "2"},
		{"timeline records", newWorkSessionTimelineCmd, "no-records", "true", "true"},
		{"update title", newWorkSessionUpdateCmd, "title", "incident", "incident"},
		{"update description", newWorkSessionUpdateCmd, "description", "triage", "triage"},
		{"update scopes", newWorkSessionUpdateCmd, "scope", "command,sudo", "[command,sudo]"},
		{"update servers", newWorkSessionUpdateCmd, "server", "web-1,web-2", "[web-1,web-2]"},
		{"update start time", newWorkSessionUpdateCmd, "starts-at", "2026-12-01T10:00:00Z", "2026-12-01T10:00:00Z"},
		{"update expiry time", newWorkSessionUpdateCmd, "expires-at", "2026-12-01T12:00:00Z", "2026-12-01T12:00:00Z"},
		{"update sudo", newWorkSessionUpdateCmd, "sudo", "id,whoami", `["id,whoami"]`},
		{"update sudo reason", newWorkSessionUpdateCmd, "sudo-reason", "incident", "incident"},
		{"use unset", newWorkSessionUseCmd, "unset", "true", "true"},
		{"list status", newWorkSessionListCmd, "status", "pending", "pending"},
		{"list requester", newWorkSessionListCmd, "requester-type", "agent", "agent"},
		{"list user", newWorkSessionListCmd, "user", "all", "all"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			first := tt.newCmd(cmdtest.NoClient, utils.Stdio)
			require.NoError(t, first.ParseFlags([]string{"--" + tt.flag + "=" + tt.value}))

			second := tt.newCmd(cmdtest.NoClient, utils.Stdio)

			assert.Equal(t, tt.want, first.Flags().Lookup(tt.flag).Value.String())
			assert.True(t, first.Flags().Changed(tt.flag), "first command must retain --%s", tt.flag)
			flag := second.Flags().Lookup(tt.flag)
			assert.Equal(t, flag.DefValue, flag.Value.String())
			assert.False(t, second.Flags().Changed(tt.flag), "second command must start without --%s", tt.flag)
		})
	}
}
