package event

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode"

	"github.com/alpacax/alpacon-cli/utils"
)

const (
	watchTimeFormat = "15:04:05"
	watchFieldEmpty = "-"
)

// Payload contents differ per event type, so only sub_type is worth reading here.
type eventFrame struct {
	EventType string `json:"event_type"`
	Payload   struct {
		SubType string `json:"sub_type"`
	} `json:"payload"`
}

// Writes nothing at all on failure, so stdout stays parseable. now and target are params
// because frames carry no common timestamp and target belongs to the subscription.
func renderEvent(w io.Writer, raw []byte, format, target string, now time.Time) error {
	if format == utils.OutputFormatJSON {
		var line bytes.Buffer
		if err := json.Compact(&line, raw); err != nil {
			return fmt.Errorf("skipped an unparseable event frame: %w", err)
		}
		_, err := fmt.Fprintf(w, "%s\n", line.Bytes())
		return err
	}

	var frame eventFrame
	if err := json.Unmarshal(raw, &frame); err != nil {
		return fmt.Errorf("skipped an unparseable event frame: %w", err)
	}
	// Checked after stripping, which also catches an escape-only value and a bare JSON
	// null: null unmarshals into the zero struct and would print as a row of dashes.
	eventType := strip(frame.EventType)
	if eventType == "" {
		return errors.New("skipped an event frame carrying no event type")
	}

	_, err := fmt.Fprintf(w, "%s\t%s\t%s\t%s\n",
		now.Format(watchTimeFormat),
		eventType,
		field(frame.Payload.SubType),
		field(target),
	)
	return err
}

// A TTY and an agent's log receive this text verbatim. Line breaks and tabs become
// spaces so a multi-line server error does not glue into one word, and format characters
// go entirely: a bidi override could render "denied" as "approved".
func strip(s string) string {
	spaced := strings.Map(func(r rune) rune {
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		default:
			return r
		}
	}, s)
	// Trimmed last so a value of only control bytes reads as absent, not as blank data.
	return strings.TrimSpace(utils.StripControlChars(utils.StripANSIEscapes(spaced)))
}

func field(s string) string {
	if stripped := strip(s); stripped != "" {
		return stripped
	}
	return watchFieldEmpty
}
