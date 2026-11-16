package api

import (
	"bytes"
	"encoding/json"
)

// The reach values an audit list's envelope states. The set is open: a server may send one
// this build does not know, which a caller must show as sent and never read as complete.
const (
	// ReachOrg is the whole workspace, within whatever window the trail documents.
	ReachOrg = "org"
	// ReachBound is the part of the workspace the reader's bindings reach.
	ReachBound = "bound"
	// ReachSelf is only the rows that name the reader.
	ReachSelf = "self"
	// ReachNone is nothing: the reader may not read the trail.
	ReachNone = "none"
)

// Reach is how far an audit list reaches for its reader, as the server stated it. It describes
// the rule that filtered the rows, not the rows: an empty page keeps its reach.
type Reach struct {
	// Raw is the first page's value as sent, nil when the key was absent or null.
	Raw json.RawMessage
	// Value is Raw as a string, or Raw's JSON text when the server sent something else.
	Value string
	// Changed says a later page stated another value, or none: each page is resolved on its
	// own, so access that changes during a multi-page read leaves the pages disagreeing.
	Changed bool
}

// Stated is false when the first page carried no reach, as on servers that predate it.
func (r Reach) Stated() bool {
	return r.Raw != nil
}

// observe folds one more page's reach into r, which holds the first page's.
func (r *Reach) observe(page Reach) {
	if page.Stated() != r.Stated() || page.Value != r.Value {
		r.Changed = true
	}
}

func parseReach(raw json.RawMessage) Reach {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return Reach{}
	}

	value := string(raw)
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		value = text
	}
	// The decoder gave each page's RawMessage its own copy, so raw can be kept as is.
	return Reach{Raw: raw, Value: value}
}
