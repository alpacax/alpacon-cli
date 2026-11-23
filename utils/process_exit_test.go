package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestProcessExitCodeIsTheStatusAParentReadsAndNeverSuccess(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name, goos string
		code, want int
	}{
		{name: "in range", goos: "linux", code: 23, want: 23},
		{name: "top of range", goos: "linux", code: 255, want: 255},
		{name: "past 8 bits", goos: "darwin", code: 3010, want: 194},
		{name: "low bits all zero", goos: "linux", code: 256, want: 1},
		{name: "zero on a failure", goos: "linux", code: 0, want: 1},
		{name: "negative", goos: "linux", code: -1, want: 255},
		{name: "windows keeps a wide code", goos: "windows", code: 3010, want: 3010},
		{name: "windows zero on a failure", goos: "windows", code: 0, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, processExitCodeOn(tt.goos, tt.code))
		})
	}
}
