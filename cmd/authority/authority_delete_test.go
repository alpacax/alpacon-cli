package authority

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestDeleteCAErrorText(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		err         error
		wantMatch   string
		wantNoMatch string
	}{
		{
			name:        "history refusal mapped",
			err:         errors.New(`{"code": "cert_authority_cannot_be_deleted"}`),
			wantMatch:   "delete the server it runs on",
			wantNoMatch: "Failed to delete the CA",
		},
		{
			name:      "unknown falls back",
			err:       errors.New("some network failure"),
			wantMatch: "Failed to delete the CA: some network failure",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := deleteCAErrorText("my-authority", tt.err)
			assert.Contains(t, got, tt.wantMatch)
			if tt.wantNoMatch != "" {
				assert.NotContains(t, got, tt.wantNoMatch)
			}
		})
	}
}
