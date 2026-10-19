package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPreScanOutputFormat(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"space form", []string{"server", "ls", "--output", "json"}, "json"},
		{"equals form", []string{"--output=json", "server", "ls"}, "json"},
		{"table value", []string{"--output", "table"}, ""},
		{"absent", []string{"server", "ls"}, ""},
		{"empty args", nil, ""},
		{"last wins json", []string{"--output", "table", "--output=json"}, "json"},
		{"last wins table", []string{"--output=json", "--output", "table"}, ""},
		{"after separator ignored", []string{"exec", "--", "echo", "--output", "json"}, ""},
		{"before separator counts", []string{"--output", "json", "--", "x"}, "json"},
		{"missing value", []string{"server", "--output"}, ""},
		{"other value", []string{"--output", "yaml"}, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, PreScanOutputFormat(tt.args))
		})
	}
}
