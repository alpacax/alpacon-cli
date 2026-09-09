package utils

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCompactStrings(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name        string
		input, want []string
	}{
		{name: "nil"}, {name: "empty", input: []string{}},
		{name: "blank", input: []string{"", " ", "\t\n"}},
		{name: "padded", input: []string{" a ", "", " b\t"}, want: []string{"a", "b"}},
		{name: "duplicates", input: []string{"a", " a ", "b"}, want: []string{"a", "a", "b"}},
	} {
		t.Run(tc.name, func(t *testing.T) { assert.Equal(t, tc.want, CompactStrings(tc.input)) })
	}
}
