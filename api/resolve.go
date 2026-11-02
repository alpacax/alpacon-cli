package api

import (
	"errors"
	"strings"

	"github.com/alpacax/alpacon-cli/client"
)

// ErrBlankName marks a resolver's refusal of a name that trims to empty, so a caller can
// tell it apart from an ordinary not-found with errors.Is instead of matching message text.
var ErrBlankName = errors.New("blank name")

// ErrNotFound marks a resolver's miss: the lookup answered and no resource has that name.
// A request that failed never carries it.
var ErrNotFound = errors.New("not found")

type notFoundError struct{ msg string }

func (e *notFoundError) Error() string        { return e.msg }
func (e *notFoundError) Is(target error) bool { return target == ErrNotFound }

// blankNameError carries a caller-chosen message while still satisfying errors.Is(err,
// ErrBlankName), so each resolver keeps its own wording without losing the shared sentinel.
type blankNameError struct{ msg string }

// ResolveByNameOptions names ResolveByName's parameters so a call site spells out each
// same-typed field, such as BlankMsg and NotFoundMsg, instead of passing them by position, where
// a swap would compile silently.
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
		return nil, &notFoundError{msg: opts.NotFoundMsg}
	}

	return &results[0], nil
}
