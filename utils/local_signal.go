package utils

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"slices"
	"strings"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"golang.org/x/term"
)

// The MCP client mirrors this command-denial gate allowlist.
const commandDenialGates = "server_reach,command_preapproval,command_acl,file_acl,risk,approval,sudo,work_session,block"

var deliverLocalSignal = func(kind localsignal.Kind, fields localsignal.Fields) {
	red := func(s string) string {
		if term.IsTerminal(int(os.Stderr.Fd())) {
			return Red(s)
		}
		return s
	}
	emitter := localsignal.New(TrackedStderr(), red, nil)
	emitter.JSON = OutputFormat == OutputFormatJSON
	emitter.Emit(kind, fields)
}

type signalError struct {
	error
	fields           localsignal.Fields
	approvalSignaled bool
}

func (e *signalError) Unwrap() error { return e.error }

func WithSignalFields(err error, fields localsignal.Fields) error {
	if err == nil {
		return nil
	}
	return &signalError{error: err, fields: fields}
}

func SignalFields(err error) localsignal.Fields {
	var context *signalError
	if errors.As(err, &context) {
		return context.fields
	}
	return localsignal.Fields{}
}

func MarkApprovalSignaled(err error) error {
	if err == nil {
		return nil
	}
	return &signalError{error: err, fields: SignalFields(err), approvalSignaled: true}
}

func SignalApprovalPending(err error, fields localsignal.Fields) {
	for e := range errorChain(err) {
		if context, ok := e.(*signalError); ok && context.approvalSignaled {
			return
		}
	}
	fields = responseSignalFields(err, fields)
	EmitLocalSignal(localsignal.ApprovalPending, fields)
}

func EmitLocalSignal(kind localsignal.Kind, fields localsignal.Fields) {
	deliverLocalSignal(kind, fields)
}

type responseRefusal struct {
	Code    string
	Gate    string
	payload map[string]any
}

func (e responseRefusal) Error() string               { return e.Code }
func (e responseRefusal) ErrorCode() string           { return e.Code }
func (e responseRefusal) ErrorSource() string         { return "" }
func (e responseRefusal) ErrorGate() string           { return e.Gate }
func (e responseRefusal) ErrorMissing() []string      { return nil }
func (e responseRefusal) ErrorFields() map[string]any { return e.payload }

// SignalResponse handles a pause on raw responses without classifying raw API calls as commands.
func SignalResponse(status int, body []byte) {
	if !IsRefusalStatus(status) {
		return
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return
	}
	code, _ := payload["code"].(string)
	SignalPaused(responseRefusal{Code: code, payload: payload})
}

// SignalRefusal is called only by command execution and file transfer paths.
// Error sinks own pause signals so one pause never increments the folded count twice.
func SignalRefusal(err error) { signalRefusal(err, true) }

// SignalPaused applies to any operation, using only the structured pause code.
func SignalPaused(err error) { signalRefusal(err, false) }

func signalRefusal(err error, command bool) {
	seen := map[struct {
		session string
		kind    localsignal.Kind
	}]bool{}
	signalOne := func(e error, fields localsignal.Fields) {
		fields.Gate = ""
		if carrier, ok := e.(interface{ ErrorGate() string }); ok {
			fields.Gate = carrier.ErrorGate()
		}
		code := ""
		if carrier, ok := e.(interface{ ErrorCode() string }); ok {
			code = carrier.ErrorCode()
		}
		kind := localsignal.Denied
		if code == "session_paused" {
			if command {
				return
			}
			kind = localsignal.Paused
		} else {
			if !command {
				return
			}
			rejected, _ := e.(interface{ RejectedCommand() bool })
			if rejected == nil || !rejected.RejectedCommand() {
				status, ok := e.(interface{ HTTPStatusCode() int })
				if !ok || status.HTTPStatusCode() != http.StatusForbidden {
					return
				}
			}
			if !commandDenial(code, fields.Gate) {
				return
			}
			if fields.Gate == "" {
				fields.Gate = "work_session"
			}
		}
		fields = responseSignalFields(e, fields)
		key := struct {
			session string
			kind    localsignal.Kind
		}{fields.Session, kind}
		if seen[key] {
			return
		}
		seen[key] = true
		EmitLocalSignal(kind, fields)
	}

	var walk func(error, localsignal.Fields)
	walk = func(e error, fields localsignal.Fields) {
		if e == nil {
			return
		}
		if context, ok := e.(*signalError); ok && context.fields != (localsignal.Fields{}) {
			fields = context.fields
		}
		signalOne(e, fields)
		switch wrapped := e.(type) {
		case interface{ Unwrap() []error }:
			for _, child := range wrapped.Unwrap() {
				walk(child, fields)
			}
		case interface{ Unwrap() error }:
			walk(wrapped.Unwrap(), fields)
		}
	}
	walk(err, localsignal.Fields{})
}

func commandDenial(code, gate string) bool {
	if gate == "" && isWorkSessionCode(code) {
		gate = "work_session"
	}
	return slices.Contains(strings.Split(commandDenialGates, ","), gate)
}

func responseSignalFields(err error, fields localsignal.Fields) localsignal.Fields {
	for e := range errorChain(err) {
		if carrier, ok := e.(interface{ ErrorFields() map[string]any }); ok {
			payload := carrier.ErrorFields()
			if link, ok := payload["console_url"].(string); ok && link != "" {
				fields.ConsoleURL = link
			}
			for _, key := range []string{"work_session_id", "work_session", "session"} {
				if id, ok := payload[key].(string); ok && id != "" {
					fields.Session = id
					break
				}
				if session, ok := payload[key].(map[string]any); ok {
					if id, ok := session["id"].(string); ok && id != "" {
						fields.Session = id
						break
					}
				}
			}
		}
	}
	return fields
}

func signalErrorsIn(args []any) {
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			SignalPaused(err)
			return
		}
	}
}
