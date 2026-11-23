package worksession

import (
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

// Source identifies where a selected WorkSession came from.
type Source uint8

const (
	SourceNone Source = iota
	SourceFlag
	SourceEnv
	SourceConfig
)

// Operation identifies the scopes one CLI operation needs.
type Operation uint8

const (
	Command Operation = iota
	FileCommand
	Websh
	Transfer
	Tunnel
)

// Need describes the server access one operation requires.
type Need struct {
	Operation Operation
	Server    string
	Account   string
}

func (n Need) scopes() []string {
	switch n.Operation {
	case FileCommand:
		return []string{utils.OperationCommand, utils.OperationWebFTP}
	case Websh:
		return []string{utils.OperationWebsh}
	case Transfer:
		return []string{utils.OperationWebFTP}
	case Tunnel:
		return []string{utils.OperationTunnel}
	default:
		return []string{utils.OperationCommand}
	}
}

// Selection holds the WorkSession an operation runs under; read ID at each request, since a settled offer replaces it.
type Selection struct {
	id       string
	source   Source
	need     Need
	ios      *utils.IOStreams
	ac       *client.AlpaconClient
	canOffer bool
	offered  bool
}

// Select resolves flag, environment, then the client's pinned workspace configuration.
func Select(ios *utils.IOStreams, ac *client.AlpaconClient, flagValue string, need Need) (*Selection, error) {
	id, source, err := resolveFor(ac.WorkspaceName, flagValue)
	if err != nil {
		return nil, err
	}
	return &Selection{id: id, source: source, need: need, ios: ios, ac: ac, canOffer: offerAllowed(readOfferEnv(ios, ac))}, nil
}

// Fixed returns a Selection that never offers to replace id.
func Fixed(id string) *Selection { return &Selection{id: id} }

// OfferedForTest returns a selection with its interactive offer enabled for command integration tests.
func OfferedForTest(ios *utils.IOStreams, ac *client.AlpaconClient, id string, need Need) *Selection {
	return &Selection{id: id, source: SourceConfig, need: need, ios: ios, ac: ac, canOffer: true}
}

// ID returns the WorkSession the next request sends.
func (s *Selection) ID() string {
	if s == nil {
		return ""
	}
	return s.id
}

// Offer adds this selection's one WorkSession refusal recovery hook when eligible.
func (s *Selection) Offer(callbacks utils.ErrorHandlerCallbacks) utils.ErrorHandlerCallbacks {
	if s == nil || !s.canOffer || s.source == SourceFlag || s.source == SourceEnv {
		return callbacks
	}
	callbacks.OnWorkSessionRefused = s.handleRefusal
	return callbacks
}
