package server

import "testing"

func TestIsValidMethod(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"token-install", true},
		{"ansible", true},
		{"", false},
		{"unknown", false},
		{"Token-Install", false}, // case-sensitive
	}
	for _, c := range cases {
		t.Run(c.input, func(t *testing.T) {
			got := isValidMethod(c.input)
			if got != c.want {
				t.Errorf("isValidMethod(%q) = %v, want %v", c.input, got, c.want)
			}
		})
	}
}

func TestValidMethodsList(t *testing.T) {
	if validMethodsList != "token-install, ansible" {
		t.Errorf("validMethodsList = %q, want %q", validMethodsList, "token-install, ansible")
	}
}
