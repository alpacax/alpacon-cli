package event

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/alpacax/alpacon-cli/api"
	"github.com/alpacax/alpacon-cli/client"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	eventSessionsURL      = "/api/events/sessions/"
	eventSubscriptionsURL = "/api/events/subscriptions/"

	// Left untyped so every const stays in one block; they remain assignable and
	// comparable to EventType.
	EventTypeSudo          = "sudo"
	EventTypeCommandOutput = "command_output"
	// Subscribed with a server id, not a command id: that is the channel
	// the server publishes it on.
	EventTypeCommandFin = "command_fin"

	// eventSessionCloseTimeout bounds one close attempt; a token renewal inside it is not bounded.
	eventSessionCloseTimeout = 10 * time.Second
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

// CloseEventSession closes an event session. The server keeps a closed session and
// answers 204 for one that is already closed.
func CloseEventSession(ctx context.Context, ac *client.AlpaconClient, id string) error {
	pathID, err := api.RequireID(id, "event session")
	if err != nil {
		return err
	}
	if _, err := ac.SendDeleteRequestWithContext(ctx, api.IDURL(eventSessionsURL, pathID, "", nil)); err != nil {
		return fmt.Errorf("failed to close event session: %w", err)
	}
	return nil
}

// closeSession tries once: a session left open only reads as open, so a failure is
// worth a debug line and nothing more.
func closeSession(ac *client.AlpaconClient, id string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := CloseEventSession(ctx, ac, id); err != nil {
		utils.CliDebug("could not close unopened event session %s: %v", id, err)
	}
}
