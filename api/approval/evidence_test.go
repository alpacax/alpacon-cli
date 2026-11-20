package approval

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func decodeEvidence(t *testing.T, field string) VerdictEvidence {
	t.Helper()
	body := `{"id":"550e8400-e29b-41d4-a716-446655440000","metadata":{"command":{"line":"sh /tmp/a.sh"` + field + `}}}`
	var req ApprovalRequest
	require.NoError(t, json.Unmarshal([]byte(body), &req))
	require.NotNil(t, req.Metadata)
	require.NotNil(t, req.Metadata.Command)
	assert.Equal(t, "sh /tmp/a.sh", req.Metadata.Command.Line, "the rest of the request must still decode")
	return req.Metadata.Command.VerdictEvidence
}

func TestVerdictEvidenceDecodesTheServerShape(t *testing.T) {
	t.Parallel()
	got := decodeEvidence(t, `,"verdict_evidence":[
		{"claim":"Downloads and runs a remote installer","ranges":[{"start_line":3,"end_line":3,"quote":"curl -fsSL https://example.com/i.sh | sh"},{"start_line":40,"end_line":41,"quote":"rm -rf"}]},
		{"claim":"Edits sudoers","ranges":[]}
	]`)

	assert.Equal(t, VerdictEvidence{
		{Claim: "Downloads and runs a remote installer", Ranges: []EvidenceRange{{StartLine: 3, EndLine: 3}, {StartLine: 40, EndLine: 41}}},
		{Claim: "Edits sudoers", Ranges: []EvidenceRange{}},
	}, got)
}

func TestVerdictEvidenceAbsentOrEmptyIsNone(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field string
	}{
		{name: "absent on an older server", field: ``},
		{name: "null", field: `,"verdict_evidence":null`},
		{name: "empty list", field: `,"verdict_evidence":[]`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Empty(t, decodeEvidence(t, tt.field))
		})
	}
}

func TestVerdictEvidenceMalformedNeverFailsTheRequest(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		field string
		want  VerdictEvidence
	}{
		{name: "a string", field: `,"verdict_evidence":"oops"`},
		{name: "an object", field: `,"verdict_evidence":{"claim":"x","ranges":[]}`},
		{name: "a number", field: `,"verdict_evidence":3`},
		{
			name:  "items that are not claims are dropped",
			field: `,"verdict_evidence":["x",null,7,{"claim":3,"ranges":[]},{"claim":"  ","ranges":[]},{"claim":"no ranges"},{"claim":"null ranges","ranges":null},{"claim":"ranges not a list","ranges":"L3"},{"claim":"kept","ranges":[]}]`,
			want:  VerdictEvidence{{Claim: "kept", Ranges: []EvidenceRange{}}},
		},
		{
			name:  "ranges that are not lines are dropped and the claim stays",
			field: `,"verdict_evidence":[{"claim":"c","ranges":["L3",null,{"start_line":0,"end_line":1},{"start_line":5,"end_line":4},{"start_line":"3","end_line":3},{"start_line":2.5,"end_line":3},{"start_line":true,"end_line":3},{"end_line":3},{"start_line":3},{"start_line":7,"end_line":9}]}]`,
			want:  VerdictEvidence{{Claim: "c", Ranges: []EvidenceRange{{StartLine: 7, EndLine: 9}}}},
		},
		{
			name:  "a claim whose ranges are all malformed is kept unlocated",
			field: `,"verdict_evidence":[{"claim":"c","ranges":[{"start_line":-1,"end_line":2}]}]`,
			want:  VerdictEvidence{{Claim: "c", Ranges: []EvidenceRange{}}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := decodeEvidence(t, tt.field)
			if tt.want == nil {
				assert.Empty(t, got)
				return
			}
			assert.Equal(t, tt.want, got)
		})
	}
}
