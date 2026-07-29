package event

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	eventSessionsURL      = "/api/events/sessions/"
	eventSubscriptionsURL = "/api/events/subscriptions/"
)

// EventType is a server event channel type, marshalled as a plain string. The server
// defines the full set; untyped literals convert, so types the CLI lacks a constant for stay usable.
type EventType string

const (
	EventTypeSudo          EventType = "sudo"
	EventTypeCommandOutput EventType = "command_output"
)

// EventSessionResponse is returned when creating an event session.
type EventSessionResponse struct {
	ID           string `json:"id"`
	WebsocketURL string `json:"websocket_url"`
	ChannelID    string `json:"channel_id"`
}

// EventSubscriptionRequest is sent to subscribe to an event type.
type EventSubscriptionRequest struct {
	Channel   string    `json:"channel"`
	EventType EventType `json:"event_type"`
	TargetID  string    `json:"target_id"`
}

// CreateEventSession creates a new event session and returns the WebSocket URL
// and channel ID for subscribing to events.
func CreateEventSession(ac *client.AlpaconClient) (*EventSessionResponse, error) {
	respBody, err := ac.SendPostRequest(eventSessionsURL, struct{}{})
	if err != nil {
		return nil, fmt.Errorf("failed to create event session: %w", err)
	}

	var resp EventSessionResponse
	if err := json.Unmarshal(respBody, &resp); err != nil {
		return nil, fmt.Errorf("failed to parse event session response: %w", err)
	}

	return &resp, nil
}

// SubscribeEvent subscribes the given channel to eventType events, scoped to
// targetID (a websh session for sudo, a command for command_output).
func SubscribeEvent(ac *client.AlpaconClient, channelID string, eventType EventType, targetID string) error {
	req := &EventSubscriptionRequest{
		Channel:   channelID,
		EventType: eventType,
		TargetID:  targetID,
	}

	_, err := ac.SendPostRequest(eventSubscriptionsURL, req)
	if err != nil {
		return fmt.Errorf("failed to subscribe to %s events: %w", eventType, err)
	}

	return nil
}
