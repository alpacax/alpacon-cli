package types

import "encoding/json"

// SentString is a nullable JSON string that remembers whether its key was in
// the body at all; Value is empty for null.
type SentString struct {
	Value string
	Sent  bool
}

func (s *SentString) UnmarshalJSON(data []byte) error {
	s.Sent = true
	s.Value = ""
	if string(data) == "null" {
		return nil
	}
	return json.Unmarshal(data, &s.Value)
}

func (s SentString) MarshalJSON() ([]byte, error) {
	if s.Value == "" {
		return []byte("null"), nil
	}
	return json.Marshal(s.Value)
}

// IsZero lets `omitzero` drop a key that was never sent while keeping a sent null.
func (s SentString) IsZero() bool { return !s.Sent }
