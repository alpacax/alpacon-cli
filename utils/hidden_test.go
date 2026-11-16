package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestJoinReached(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		names  []string
		hidden *int
		noun   string
		want   string
	}{
		{"nil count", []string{"web-1", "db-1"}, nil, "server", "web-1, db-1"},
		{"zero count", []string{"web-1"}, new(0), "server", "web-1"},
		{"negative count", []string{"web-1"}, new(-1), "server", "web-1"},
		{"nothing at all", nil, nil, "server", ""},
		{"names and one hidden", []string{"web-1"}, new(1), "server", "web-1 and 1 more"},
		{"names and two hidden", []string{"web-1", "db-1"}, new(2), "server", "web-1, db-1 and 2 more"},
		{"empty and one hidden", nil, new(1), "server", "1 server you cannot access"},
		{"empty and two hidden", []string{}, new(2), "server", "2 servers you cannot access"},
		{"cluster noun", nil, new(1), "cluster", "1 cluster you cannot access"},
		{"blank name and one hidden", []string{"  "}, new(1), "server", "1 server you cannot access"},
		{"control only name and one hidden", []string{"\x1b[2J"}, new(1), "server", "1 server you cannot access"},
		{"blank name dropped beside a name", []string{"", "web-1"}, new(1), "server", "web-1 and 1 more"},
		{"blank name and zero count unchanged", []string{"", "web-1"}, new(0), "server", ", web-1"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, JoinReached(tt.names, tt.hidden, tt.noun))
		})
	}
}

func TestHiddenCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		hidden *int
		want   int
	}{
		{"nil", nil, 0},
		{"positive", new(3), 3},
		{"negative", new(-2), 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, HiddenCount(tt.hidden))
		})
	}
}
