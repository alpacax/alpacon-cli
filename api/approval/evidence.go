package approval

import (
	"encoding/json"
	"strings"
)

// VerdictEvidence is the claims behind the AI's verdict on a file execution,
// each tied to lines of FileExecution.Content. The server sends it only to a
// viewer who may decide the request and is not its requester.
//
// It decodes leniently, because it only helps the approver read the script: a
// value that is not a list reads as no evidence, an item without a non-blank
// claim and a ranges list is dropped, and so is a range that is not a pair of
// line numbers. A claim left with no range stays, unlocated, as the server
// keeps one.
type VerdictEvidence []EvidenceClaim

// EvidenceClaim is one claim and the script lines it rests on. An empty Ranges
// means the claim could not be placed in the script.
type EvidenceClaim struct {
	Claim  string
	Ranges []EvidenceRange
}

// EvidenceRange is a span of 1-based script lines, both ends included.
type EvidenceRange struct {
	StartLine int `json:"start_line"`
	EndLine   int `json:"end_line"`
}

func (v *VerdictEvidence) UnmarshalJSON(data []byte) error {
	*v = nil
	var items []json.RawMessage
	if json.Unmarshal(data, &items) != nil {
		return nil
	}
	for _, item := range items {
		var c struct {
			Claim  string            `json:"claim"`
			Ranges []json.RawMessage `json:"ranges"`
		}
		if json.Unmarshal(item, &c) != nil || strings.TrimSpace(c.Claim) == "" || c.Ranges == nil {
			continue
		}
		claim := EvidenceClaim{Claim: c.Claim, Ranges: []EvidenceRange{}}
		for _, raw := range c.Ranges {
			var r EvidenceRange
			if json.Unmarshal(raw, &r) != nil || r.StartLine < 1 || r.EndLine < r.StartLine {
				continue
			}
			claim.Ranges = append(claim.Ranges, r)
		}
		*v = append(*v, claim)
	}
	return nil
}
