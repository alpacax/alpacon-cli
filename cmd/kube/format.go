package kube

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/alpacax/alpacon-cli/utils"
)

// isSurfaceOff reports whether err carries a 404, which the server answers for
// every path under /api/kubernetes/ when the Kubernetes surface is off.
func isSurfaceOff(err error) bool {
	return utils.HTTPStatusCode(err) == http.StatusNotFound
}

// formatJSONValue renders a free-form JSON field on one line; null or absent is blank.
func formatJSONValue(raw json.RawMessage) string {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return ""
	}
	var compact bytes.Buffer
	if err := json.Compact(&compact, trimmed); err != nil {
		return string(trimmed)
	}
	return compact.String()
}

// formatTags renders tags as "k=v" sorted by key, strings bare and other values as compact JSON;
// anything that is not an object falls back to formatJSONValue.
func formatTags(raw json.RawMessage) string {
	var tags map[string]json.RawMessage
	if err := json.Unmarshal(raw, &tags); err != nil || tags == nil {
		return formatJSONValue(raw)
	}

	keys := make([]string, 0, len(tags))
	for k := range tags {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	pairs := make([]string, 0, len(keys))
	for _, k := range keys {
		var s string
		if err := json.Unmarshal(tags[k], &s); err == nil {
			pairs = append(pairs, fmt.Sprintf("%s=%s", k, s))
			continue
		}
		pairs = append(pairs, fmt.Sprintf("%s=%s", k, formatJSONValue(tags[k])))
	}
	return strings.Join(pairs, ", ")
}

func formatTime(t *time.Time) string {
	if t == nil || t.IsZero() {
		return ""
	}
	return t.Local().Format("2006-01-02 15:04")
}
