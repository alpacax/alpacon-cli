package iam

// jsonSlice returns s, or an empty slice when it is nil.
//
// A hand-built JSON document has to carry [] rather than null for an empty list,
// which is the contract utils.PrintTable already applies to every list command. A
// nil Go slice marshals to null, so the commands that assemble their own JSON
// object out of several projections pass each one through here.
func jsonSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}
