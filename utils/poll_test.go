package utils

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNextPollTick(t *testing.T) {
	base := time.Second
	tests := []struct {
		name    string
		elapsed time.Duration
		want    time.Duration
	}{
		{name: "base tick while the command is young", elapsed: 0, want: base},
		{name: "still base at the end of the fast window", elapsed: 9 * base, want: base},
		{name: "widens once the fast window closes", elapsed: 10 * base, want: 5 * base},
		{name: "holds through the medium window", elapsed: 59 * base, want: 5 * base},
		{name: "widest once the medium window closes", elapsed: 60 * base, want: 10 * base},
		{name: "stays widest", elapsed: 3000 * base, want: 10 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextPollTick(base, tt.elapsed))
		})
	}
}

func TestNextPollBackoff(t *testing.T) {
	base := time.Second
	tests := []struct {
		name       string
		attempt    int
		retryAfter time.Duration
		want       time.Duration
	}{
		{name: "first failure keeps the base tick", attempt: 0, want: base},
		{name: "doubles per consecutive failure", attempt: 1, want: 2 * base},
		{name: "still doubling", attempt: 3, want: 8 * base},
		{name: "capped once doubling passes the cap", attempt: 6, want: 60 * base},
		{name: "stays capped", attempt: 20, want: 60 * base},
		{name: "server's Retry-After wins over the schedule", attempt: 0, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After wins even when it is shorter", attempt: 5, retryAfter: 3 * base, want: 3 * base},
		{name: "Retry-After is capped too", attempt: 0, retryAfter: 3000 * base, want: 60 * base},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, NextPollBackoff(base, tt.attempt, tt.retryAfter))
		})
	}
}
