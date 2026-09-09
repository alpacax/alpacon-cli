package utils

import (
	"math"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfter accepts delta-seconds, as sent by DRF throttling, not HTTP dates.
func ParseRetryAfter(value string) time.Duration {
	seconds, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
	if err != nil || seconds <= 0 || seconds > math.MaxInt64/int64(time.Second) {
		return 0
	}
	return time.Duration(seconds) * time.Second
}
