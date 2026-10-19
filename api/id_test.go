package api

import (
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var invalidIDCases = []struct {
	name string
	id   string
}{
	{name: "empty", id: ""},
	{name: "whitespace only", id: "   "},
	{name: "dot", id: "."},
	{name: "dot dot", id: ".."},
	{name: "parent traversal", id: "../../etc/test"},
	{name: "embedded slash", id: "acme/widgets"},
	{name: "backslash", id: `..\etc`},
	{name: "query", id: "id?x=1"},
	{name: "fragment", id: "id#frag"},
	{name: "percent escape", id: "acme%2Fwidgets"},
	{name: "semicolon", id: "acme;widgets"},
	{name: "control character", id: "id\nx"},
}

var validIDCases = []struct {
	name string
	id   string
	want string
}{
	{name: "uuid", id: "550e8400-e29b-41d4-a716-446655440000", want: "550e8400-e29b-41d4-a716-446655440000"},
	{name: "integer", id: "42", want: "42"},
	{name: "slug", id: "ses-abc123", want: "ses-abc123"},
	{name: "surrounding whitespace is trimmed", id: " 42 ", want: "42"},
	{name: "dots inside a segment", id: "v1.2", want: "v1.2"},
}

func TestRequireIDRefusesValuesThatWouldRewriteThePath(t *testing.T) {
	t.Parallel()

	for _, tc := range invalidIDCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := RequireID(tc.id, "work session")

			require.ErrorIs(t, err, utils.ErrInvalidID)
			assert.Empty(t, got)
			assert.Contains(t, err.Error(), "work session")
		})
	}
}

func TestRequireIDAcceptsSinglePathSegments(t *testing.T) {
	t.Parallel()

	for _, tc := range validIDCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := RequireID(tc.id, "work session")

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestRequireIDCarriesUsageCodeWithoutParsingTheID(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		id   string
		want string
	}{
		{name: "code", id: "x; code: auth_mfa_required", want: `invalid work session ID "x; code: auth_mfa_required": must be a single path segment`},
		{name: "code and source", id: "../x; code: auth_mfa_required; source: forged", want: `invalid work session ID "../x; code: auth_mfa_required; source: forged": must be a single path segment`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := RequireID(tc.id, "work session")

			require.ErrorIs(t, err, utils.ErrInvalidID)
			assert.Equal(t, tc.want, err.Error())
			code, source := utils.ParseErrorResponse(err)
			assert.Equal(t, utils.UsageErrorCode, code)
			assert.Empty(t, source)
		})
	}
}

func TestRequireResponseIDRefusesValuesThatWouldRewriteThePath(t *testing.T) {
	t.Parallel()

	for _, tc := range invalidIDCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := RequireResponseID(tc.id, "work session")

			require.ErrorIs(t, err, ErrUnusableID)
			require.NotErrorIs(t, err, utils.ErrInvalidID)
			assert.Empty(t, got)
			assert.Equal(t, "the server returned an unusable work session ID", err.Error())
			if tc.id != "" {
				assert.NotContains(t, err.Error(), tc.id)
			}
		})
	}
}

func TestRequireResponseIDAcceptsSinglePathSegments(t *testing.T) {
	t.Parallel()

	for _, tc := range validIDCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := RequireResponseID(tc.id, "work session")

			require.NoError(t, err)
			assert.Equal(t, tc.want, got.String())
		})
	}
}

func TestRequireResponseIDDoesNotExposeAPlantedErrorCode(t *testing.T) {
	t.Parallel()

	_, err := RequireResponseID("x; code: work_session_required", "work session")

	require.ErrorIs(t, err, ErrUnusableID)
	require.NotErrorIs(t, err, utils.ErrInvalidID)
	assert.NotContains(t, err.Error(), "x; code: work_session_required")
	code, source := utils.ParseErrorResponse(err)
	assert.Empty(t, code)
	assert.Empty(t, source)
}

func TestIDURLJoinsTheCheckedIDAndAction(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name   string
		id     string
		action string
		params map[string]string
		want   string
	}{
		{name: "id only", id: "42", want: "https://alpacon.test/api/notes/42/"},
		{name: "id and action", id: " ses-1 ", action: "close", want: "https://alpacon.test/api/notes/ses-1/close/"},
		{name: "query", id: "42", action: "timeline", params: map[string]string{"page": "2"}, want: "https://alpacon.test/api/notes/42/timeline/?page=2"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			id, err := RequireID(tc.id, "note")
			require.NoError(t, err)

			got := IDURL("https://alpacon.test/api/notes/", id, tc.action, tc.params)

			assert.Equal(t, tc.want, got)
		})
	}
}

func TestIDURLPanicsOnAnUncheckedPathID(t *testing.T) {
	t.Parallel()

	assert.Panics(t, func() { IDURL("https://alpacon.test/api/notes/", PathID{}, "", nil) })
}
