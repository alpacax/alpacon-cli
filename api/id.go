package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/alpacax/alpacon-cli/utils"
)

// ErrUnusableID marks an ID from a server response that cannot safely be used as a path segment.
var ErrUnusableID = errors.New("unusable ID in server response")

type invalidIDError struct {
	id   string
	what string
}

func (e *invalidIDError) Error() string {
	return fmt.Sprintf("invalid %s ID %q: must be a single path segment", e.what, e.id)
}
func (e *invalidIDError) Unwrap() error       { return utils.ErrInvalidID }
func (e *invalidIDError) ErrorCode() string   { return utils.UsageErrorCode }
func (e *invalidIDError) ErrorSource() string { return "" }

type unusableResponseIDError struct{ what string }

func (e *unusableResponseIDError) Error() string {
	return fmt.Sprintf("the server returned an unusable %s ID", e.what)
}
func (e *unusableResponseIDError) Unwrap() error { return ErrUnusableID }

// RequireID trims id and refuses anything that is not one URL path segment. BuildURL's
// path.Join resolves "..", and a server or proxy may decode "%2F", so either would retarget the request.
func RequireID(id, what string) (string, error) {
	id, ok := pathSegmentID(id)
	if !ok {
		return "", &invalidIDError{id: id, what: what}
	}
	return id, nil
}

// RequireResponseID trims an ID from a server response and refuses it unless it is one URL path segment.
func RequireResponseID(id, what string) (string, error) {
	id, ok := pathSegmentID(id)
	if !ok {
		return "", &unusableResponseIDError{what: what}
	}
	return id, nil
}

func pathSegmentID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	valid := id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\?#%;`) && !strings.ContainsFunc(id, unicode.IsControl)
	return id, valid
}
