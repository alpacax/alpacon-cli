package exec

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPowershellNestingWarning(t *testing.T) {
	tests := []struct {
		name     string
		osName   string
		command  string
		wantWarn bool
	}{
		{
			name:     "windows nested powershell -Command warns",
			osName:   "Windows Server 2019",
			command:  `powershell -Command 'Write-Output "a|b"'`,
			wantWarn: true,
		},
		{
			name:     "powershell.exe with -NoProfile then -Command warns",
			osName:   "Windows",
			command:  `powershell.exe -NoProfile -Command 'Write-Output "a"'`,
			wantWarn: true,
		},
		{
			name:     "pwsh -c warns",
			osName:   "windows",
			command:  `pwsh -c 'Get-Service'`,
			wantWarn: true,
		},
		{
			name:     "pwsh.exe -Command warns",
			osName:   "Windows 11",
			command:  `pwsh.exe -Command 'Get-Service'`,
			wantWarn: true,
		},
		{
			name:     "case-insensitive command and flag",
			osName:   "Windows",
			command:  `POWERSHELL -COMMAND 'Get-Service'`,
			wantWarn: true,
		},
		{
			name:     "no powershell prefix does not warn",
			osName:   "Windows",
			command:  `Get-Service | Where-Object Name -match "a|b"`,
			wantWarn: false,
		},
		{
			name:     "powershell without -Command does not warn",
			osName:   "Windows",
			command:  `powershell script.ps1`,
			wantWarn: false,
		},
		{
			name:     "non-windows does not warn",
			osName:   "Ubuntu 22.04",
			command:  `powershell -Command 'Get-Service'`,
			wantWarn: false,
		},
		{
			name:     "empty os does not warn",
			osName:   "",
			command:  `powershell -Command 'Get-Service'`,
			wantWarn: false,
		},
		{
			name:     "empty command does not warn",
			osName:   "Windows",
			command:  ``,
			wantWarn: false,
		},
		{
			name:     "powershell substring in real command does not warn",
			osName:   "Windows",
			command:  `Get-Item C:\powershell\notes.txt`,
			wantWarn: false,
		},
		{
			name:     "-c as script argument does not warn",
			osName:   "Windows",
			command:  `powershell script.ps1 -c foo`,
			wantWarn: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := powershellNestingWarning(tt.osName, tt.command)
			if tt.wantWarn {
				assert.NotEmpty(t, got)
				assert.True(t, strings.Contains(got, "PowerShell"), "warning should mention PowerShell")
			} else {
				assert.Empty(t, got)
			}
		})
	}
}

func TestCommandNestsPowershell(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    bool
	}{
		{"powershell -Command", `powershell -Command 'Get-Service'`, true},
		{"powershell.exe -NoProfile -Command", `powershell.exe -NoProfile -Command 'x'`, true},
		{"pwsh -c", `pwsh -c 'x'`, true},
		{"case-insensitive", `POWERSHELL -COMMAND 'x'`, true},
		{"no powershell prefix", `Get-Service | Where-Object Name -match "a|b"`, false},
		{"powershell without -Command", `powershell script.ps1`, false},
		{"empty command", ``, false},
		{"powershell substring in path", `Get-Item C:\powershell\notes.txt`, false},
		{"-c as script argument after script path", `powershell script.ps1 -c foo`, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, commandNestsPowershell(tt.command))
		})
	}
}
