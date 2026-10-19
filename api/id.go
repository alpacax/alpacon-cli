package api

import (
	"errors"
	"fmt"
	"path"
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

// PathID is an ID that passed RequireID or RequireResponseID, the only way to make one outside this package.
type PathID struct{ s string }

func (id PathID) String() string { return id.s }

// RequireID trims id and refuses anything that is not one URL path segment. BuildURL's
// path.Join resolves "..", and a server or proxy may decode "%2F", so either would retarget the request.
func RequireID(id, what string) (PathID, error) {
	id, ok := pathSegmentID(id)
	if !ok {
		return PathID{}, &invalidIDError{id: id, what: what}
	}
	return PathID{s: id}, nil
}

// RequireResponseID trims an ID from a server response and refuses it unless it is one URL path segment.
func RequireResponseID(id, what string) (PathID, error) {
	id, ok := pathSegmentID(id)
	if !ok {
		return PathID{}, &unusableResponseIDError{what: what}
	}
	return PathID{s: id}, nil
}

// IDURL builds base/id/action/ with params as the query. action is a constant such as "close", or "" for none.
func IDURL(base string, id PathID, action string, params map[string]string) string {
	if id.s == "" {
		panic("api.IDURL: zero PathID")
	}
	return utils.BuildURL(base, path.Join(id.s, action), params)
}

func pathSegmentID(id string) (string, bool) {
	id = strings.TrimSpace(id)
	valid := id != "" && id != "." && id != ".." && !strings.ContainsAny(id, `/\?#%;`) && !strings.ContainsFunc(id, unicode.IsControl)
	return id, valid
}
