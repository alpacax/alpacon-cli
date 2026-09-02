package iam

// jsonSlice swaps a nil slice for an empty one so hand-built JSON documents carry [] for
// an empty list, matching what utils.PrintTable gives every list command.
func jsonSlice[T any](s []T) []T {
	if s == nil {
		return []T{}
	}

	return s
}
