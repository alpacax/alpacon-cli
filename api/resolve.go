package api

import (
	"errors"
	"strings"

	"github.com/alpacax/alpacon-cli/client"
)

// ErrBlankName marks a resolver's refusal of a name that trims to empty, so a caller can
// tell it apart from an ordinary not-found with errors.Is instead of matching message text.
var ErrBlankName = errors.New("blank name")

// blankNameError carries a caller-chosen message while still satisfying errors.Is(err,
// ErrBlankName), so each resolver keeps its own wording without losing the shared sentinel.
type blankNameError struct{ msg string }

func (e *blankNameError) Error() string        { return e.msg }
func (e *blankNameError) Is(target error) bool { return target == ErrBlankName }

// ResolveByName looks up a single resource through a server-side, exact-match name filter.
// A blank name is refused up front—an empty filter comes back as the whole list, not "found".
func ResolveByName[T any](ac *client.AlpaconClient, endpoint, filterKey, name, blankMsg, notFoundMsg string) (*T, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, &blankNameError{msg: blankMsg}
	}

	results, err := FetchPagesUpTo[T](ac, endpoint, map[string]string{filterKey: name}, 1)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, errors.New(notFoundMsg)
	}

	return &results[0], nil
}
