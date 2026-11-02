package utils

import (
	"encoding/json"
	"errors"
	"os"

	"github.com/alpacax/alpacon-cli/pkg/localsignal"
	"golang.org/x/term"
)

var deliverLocalSignal = func(kind localsignal.Kind, fields localsignal.Fields) {
	red := func(s string) string {
		if term.IsTerminal(int(os.Stderr.Fd())) {
			return Red(s)
		}
		return s
	}
	localsignal.New(TrackedStderr(), red, CliDebug).Emit(kind, fields)
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

// SignalResponse covers the raw API path, which deliberately preserves the response body.
func SignalResponse(status int, body []byte) {
	if !IsRefusalStatus(status) {
		return
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return
	}
	code, _ := payload["code"].(string)
	gate, _ := payload["gate"].(string)
	SignalRefusal(responseRefusal{Code: code, Gate: gate, payload: payload})
}

// SignalRefusal uses structured fields only: message text cannot create a denial signal.
func SignalRefusal(err error) {
	fallback := SignalFields(err)
	for e := range errorChain(err) {
		fields := fallback
		if context := SignalFields(e); context != (localsignal.Fields{}) {
			fields = context
		}
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
			kind = localsignal.Paused
		} else if fields.Gate == "" {
			continue
		}
		if carrier, ok := e.(interface{ ErrorFields() map[string]any }); ok {
			payload := carrier.ErrorFields()
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
		EmitLocalSignal(kind, fields)
	}
}

func signalErrorsIn(args []any) {
	for _, arg := range args {
		if err, ok := arg.(error); ok {
			SignalRefusal(err)
			return
		}
	}
}
