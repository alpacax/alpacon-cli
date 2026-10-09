package kube

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/alpacax/alpacon-cli/api/kubernetes"
	"github.com/stretchr/testify/assert"
)

func TestFormatTags(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "absent", raw: "", expected: ""},
		{name: "null", raw: "null", expected: ""},
		{name: "empty object", raw: "{}", expected: ""},
		{name: "sorted by key", raw: `{"team":"infra","env":"prod"}`, expected: "env=prod, team=infra"},
		{name: "non-string values as JSON", raw: `{"tier":1,"pci":true,"meta":{"a": 1}}`, expected: `meta={"a":1}, pci=true, tier=1`},
		{name: "bare key with empty value", raw: `{"edge":""}`, expected: "edge="},
		{name: "not an object", raw: `["a", "b"]`, expected: `["a","b"]`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatTags(json.RawMessage(tt.raw)))
		})
	}
}

func TestFormatJSONValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		raw      string
		expected string
	}{
		{name: "absent", raw: "", expected: ""},
		{name: "null", raw: "null", expected: ""},
		{name: "null with whitespace", raw: " null\n", expected: ""},
		{name: "object compacted", raw: "{\n  \"phase\": \"ready\"\n}", expected: `{"phase":"ready"}`},
		{name: "string", raw: `"ready"`, expected: `"ready"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, formatJSONValue(json.RawMessage(tt.raw)))
		})
	}
}

// withLocalZone pins time.Local so expected cells are literals, not formatTime's own expression;
// it reassigns a process-wide value, so its callers stay serial.
func withLocalZone(t *testing.T) {
	t.Helper()
	orig := time.Local
	time.Local = time.FixedZone("KST", 9*60*60)
	t.Cleanup(func() { time.Local = orig })
}

func TestFormatTime(t *testing.T) {
	withLocalZone(t)
	ts := time.Date(2026, 9, 30, 8, 15, 42, 0, time.UTC)

	assert.Empty(t, formatTime(nil))
	assert.Empty(t, formatTime(&time.Time{}))
	assert.Equal(t, "2026-09-30 17:15", formatTime(&ts))
}

type statusErr struct{ code int }

func (e statusErr) Error() string       { return fmt.Sprintf("status %d", e.code) }
func (e statusErr) HTTPStatusCode() int { return e.code }

func TestIsSurfaceOff(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{name: "not found", err: statusErr{http.StatusNotFound}, expected: true},
		{name: "wrapped not found", err: fmt.Errorf("fetching page 1: %w", statusErr{http.StatusNotFound}), expected: true},
		{name: "server error", err: statusErr{http.StatusInternalServerError}},
		{name: "forbidden", err: statusErr{http.StatusForbidden}},
		{name: "no status", err: errors.New("connection refused")},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isSurfaceOff(tt.err))
		})
	}
}

func TestDescribeRows(t *testing.T) {
	withLocalZone(t)
	version := "v1.31.2"
	nodes := 3
	seen := time.Date(2026, 9, 30, 8, 15, 0, 0, time.UTC)
	added := time.Date(2026, 9, 18, 1, 2, 0, 0, time.UTC)

	tests := []struct {
		name     string
		cluster  kubernetes.ClusterDetail
		groups   []string
		expected []describeRow
	}{
		{
			name: "measured cluster",
			cluster: kubernetes.ClusterDetail{
				ID: "c-prod", Name: "prod-east", Provider: "eks", KubeVersion: &version, AgentVersion: "0.1.0",
				NodeCount: &nodes, Status: json.RawMessage(`{"phase":"ready"}`), Enabled: true,
				Tags: json.RawMessage(`{"env":"prod"}`), IsConnected: true, LastConnectivity: &seen,
				AddedAt: added, UpdatedAt: seen,
			},
			groups: []string{"platform", "sre"},
			expected: []describeRow{
				{"ID", "c-prod"},
				{"Name", "prod-east"},
				{"Provider", "eks"},
				{"Kube version", "v1.31.2"},
				{"Agent version", "0.1.0"},
				{"Nodes", "3"},
				{"Connected", "true"},
				{"Enabled", "true"},
				{"Status", `{"phase":"ready"}`},
				{"Groups", "platform, sre"},
				{"Tags", "env=prod"},
				{"Last connectivity", "2026-09-30 17:15"},
				{"Last synced at", ""},
				{"Added at", "2026-09-18 10:02"},
				{"Updated at", "2026-09-30 17:15"},
			},
		},
		{
			name: "not yet measured",
			cluster: kubernetes.ClusterDetail{
				ID: "c-new", Name: "fresh", Provider: "unknown", Status: json.RawMessage("null"),
				Tags: json.RawMessage("{}"), AddedAt: added, UpdatedAt: added,
			},
			expected: []describeRow{
				{"ID", "c-new"},
				{"Name", "fresh"},
				{"Provider", "unknown"},
				{"Kube version", ""},
				{"Agent version", ""},
				{"Nodes", ""},
				{"Connected", "false"},
				{"Enabled", "false"},
				{"Status", ""},
				{"Groups", ""},
				{"Tags", ""},
				{"Last connectivity", ""},
				{"Last synced at", ""},
				{"Added at", "2026-09-18 10:02"},
				{"Updated at", "2026-09-18 10:02"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, describeRows(&tt.cluster, tt.groups))
		})
	}
}
