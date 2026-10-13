package api

import (
	"errors"
	"fmt"
	"strings"
	"unicode"

	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// ErrBlankName marks a resolver's refusal of a name that trims to empty, so a caller can
// tell it apart from an ordinary not-found with errors.Is instead of matching message text.
var ErrBlankName = errors.New("blank name")

// blankNameError carries a caller-chosen message while still satisfying errors.Is(err,
// ErrBlankName), so each resolver keeps its own wording without losing the shared sentinel.
type blankNameError struct{ msg string }

// ResolveByNameOptions names ResolveByName's parameters so a call site cannot swap two
// same-typed fields (e.g. blankMsg/notFoundMsg) without the compiler seeing a field name change.
type ResolveByNameOptions struct {
	Endpoint    string
	FilterKey   string
	Name        string
	BlankMsg    string
	NotFoundMsg string
}

func (e *blankNameError) Error() string        { return e.msg }
func (e *blankNameError) Is(target error) bool { return target == ErrBlankName }

// RequireName trims name and refuses it if empty, returning an error satisfying
// errors.Is(err, ErrBlankName) with blankMsg as its message.
func RequireName(name, blankMsg string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &blankNameError{msg: blankMsg}
	}
	return name, nil
}

// ErrInvalidID is the sentinel every RequireID refusal wraps; it is utils.ErrInvalidID.
var ErrInvalidID = utils.ErrInvalidID

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

// RequireID trims id and refuses anything that is not one URL path segment. BuildURL's
// path.Join resolves "..", and a server or proxy may decode "%2F", so either would retarget the request.
func RequireID(id, what string) (string, error) {
	id = strings.TrimSpace(id)
	if id == "" || id == "." || id == ".." || strings.ContainsAny(id, `/\?#%;`) || strings.ContainsFunc(id, unicode.IsControl) {
		return "", &invalidIDError{id: id, what: what}
	}
	return id, nil
}

// ResolveByName looks up a single resource through a server-side, exact-match name filter.
// A blank name is refused up front—an empty filter comes back as the whole list, not "found".
func ResolveByName[T any](ac *client.AlpaconClient, opts ResolveByNameOptions) (*T, error) {
	name, err := RequireName(opts.Name, opts.BlankMsg)
	if err != nil {
		return nil, err
	}

	results, err := FetchPagesUpTo[T](ac, opts.Endpoint, map[string]string{opts.FilterKey: name}, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New(opts.NotFoundMsg)
	}

	return &results[0], nil
}
