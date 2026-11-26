package worksession

import (
	"context"
	"sync"

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
	Command   string // the alpacon command the user ran, when Operation names another
}

// command names the alpacon command the user ran for the operation, which the resend repeats.
func (n Need) command() string {
	if n.Command != "" {
		return n.Command
	}
	switch n.Operation {
	case Websh:
		return "websh"
	case Transfer:
		return "cp"
	case Tunnel:
		return "tunnel"
	default:
		return "exec"
	}
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
	resume   string
	resumeBy string // the command resume runs, empty when it is need's own
	// interrupt replaces the Ctrl+C context in a test.
	interrupt func() (context.Context, context.CancelFunc)
	// reads counts prompt reads still running, so a test can wait out one Ctrl+C left behind.
	reads sync.WaitGroup
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

// ResumeWith replaces "re-run this command" in the resume guidance, for an operation whose earlier
// steps a re-run would repeat.
func (s *Selection) ResumeWith(step string) {
	s.ResumeWithCommand(step, "")
}

// ResumeWithCommand is ResumeWith for a step that runs command instead of the one the user ran.
func (s *Selection) ResumeWithCommand(step, command string) {
	if s != nil {
		s.resume, s.resumeBy = step, command
	}
}

func (s *Selection) resumeCommand() string {
	if s.resumeBy == "" {
		return s.need.command()
	}
	return s.resumeBy
}

func (s *Selection) resumeStep() string {
	if s.resume == "" {
		return "re-run this command"
	}
	return s.resume
}

// Offer adds this selection's one WorkSession refusal recovery hook when eligible.
func (s *Selection) Offer(callbacks utils.ErrorHandlerCallbacks) utils.ErrorHandlerCallbacks {
	if s == nil || !s.canOffer || s.source == SourceFlag || s.source == SourceEnv {
		return callbacks
	}
	callbacks.OnWorkSessionRefused = s.handleRefusal
	return callbacks
}
