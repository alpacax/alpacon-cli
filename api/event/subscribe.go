package event

import (
	"encoding/json"
	"fmt"

	"github.com/alpacax/alpacon-cli/client"
)

const (
	eventSessionsURL      = "/api/events/sessions/"
	eventSubscriptionsURL = "/api/events/subscriptions/"

	// Left untyped so every const stays in one block; they remain assignable and
	// comparable to EventType.
	EventTypeSudo          = "sudo"
	EventTypeCommandOutput = "command_output"
	// Subscribed with a server id, not a command id: that is the channel
	// alpacon-server publishes it on.
	EventTypeCommandFin = "command_fin"
)

// EventType is a server event channel type, marshalled as a plain string. The server
// defines the full set; untyped literals convert, so types the CLI lacks a constant for stay usable.
type EventType string

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
	// Omitted rather than sent empty: the server validates target_id as a UUID.
	TargetID string `json:"target_id,omitempty"`
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

// SubscribeEvent subscribes the given channel to eventType events, scoped to targetID
// (a websh session for sudo, a command for command_output, a work session for
// work_session). An empty targetID is omitted, which only some event types allow.
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
