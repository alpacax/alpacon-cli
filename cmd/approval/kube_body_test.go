package approval

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/alpacax/alpacon-cli/utils"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFoldKubeBody(t *testing.T) {
	t.Parallel()
	large := strings.Repeat("x", 257)
	limit := strings.Repeat("y", 256)
	escaped := strings.Repeat(`\u003c`, 43)
	const key = `"kubectl.kubernetes.io/last-applied-configuration"`
	const pointerKey = "kubectl.kubernetes.io~1last-applied-configuration"
	const marker = "<folded: 257 bytes>"
	annotation := key + `:"` + large + `"`
	foldedAnnotation := key + `:"` + marker + `"`
	const metadataPath = "/metadata/annotations"
	object := `{"metadata":{"annotations":{` + annotation + `}}}`
	patch := `{"op":"replace","path":"` + metadataPath + "/" + pointerKey + `","value":"` + large + `"}`
	const yaml = "metadata:\n  name: web\n"
	const malformed = `{"spec":{"missing":}}`
	tests := []struct {
		name          string
		body          string
		want          string
		folded        int
		unformatted   bool
		duplicateKeys []string
	}{
		{name: "root object annotation", body: object, want: strings.Replace(object, annotation, foldedAnnotation, 1), folded: 1},
		{
			name:   "nested template annotation",
			body:   `{"spec":{"template":` + object + `}}`,
			want:   `{"spec":{"template":` + strings.Replace(object, annotation, foldedAnnotation, 1) + `}}`,
			folded: 1,
		},
		{name: "at the byte limit", body: `{"metadata":{"annotations":{` + key + `:"` + limit + `"}}}`},
		{name: "another annotation", body: `{"metadata":{"annotations":{"nginx.ingress.kubernetes.io/configuration-snippet":"` + large + `"}}}`},
		{name: "annotation with a nonstring value", body: `{"metadata":{"annotations":{` + key + `:{"field":"` + large + `"}}}}`},
		{name: "spec annotations", body: `{"spec":{"annotations":{` + annotation + `}}}`},
		{name: "top level annotations", body: `{"annotations":{` + annotation + `}}`},
		{name: "array index breaks the annotation path", body: `{"metadata":{"annotations":[{` + annotation + `}]}}`},
		{
			name:   "patch string value",
			body:   "[" + patch + "]",
			want:   "[" + strings.Replace(patch, large, marker, 1) + "]",
			folded: 1,
		},
		{
			name:   "patch annotations before path",
			body:   `[{"value":{` + annotation + `,"keep":"` + large + `"},"path":"/metadata/annotations","op":"add"}]`,
			want:   `[{"value":{` + foldedAnnotation + `,"keep":"` + large + `"},"path":"/metadata/annotations","op":"add"}]`,
			folded: 1,
		},
		{
			name:   "patch metadata object",
			body:   `[{"op":"add","path":"/metadata","value":{"annotations":{` + annotation + `}}}]`,
			want:   `[{"op":"add","path":"/metadata","value":{"annotations":{` + foldedAnnotation + `}}}]`,
			folded: 1,
		},
		{
			name:   "patch nested template annotations",
			body:   `[{"op":"add","path":"/spec/template/metadata/annotations","value":{` + annotation + `}}]`,
			want:   `[{"op":"add","path":"/spec/template/metadata/annotations","value":{` + foldedAnnotation + `}}]`,
			folded: 1,
		},
		{
			name:   "patch with multiple spans in source order",
			body:   `[{"op":"add","value":[` + object + "," + object + `],"path":"/items"},` + patch + "]",
			want:   `[{"op":"add","value":[` + strings.ReplaceAll(object+","+object, annotation, foldedAnnotation) + `],"path":"/items"},` + strings.Replace(patch, large, marker, 1) + "]",
			folded: 3,
		},
		{name: "patch data annotations", body: `[{"op":"add","path":"/data/annotations","value":{` + annotation + `}}]`},
		{name: "patch spec annotations", body: `[{"op":"add","path":"/spec/annotations","value":{` + annotation + `}}]`},
		{name: "patch escaped tilde cannot look like slash", body: strings.Replace("["+patch+"]", "~1", "~01", 1)},
		{name: "patch malformed pointer", body: strings.Replace("["+patch+"]", "/metadata", "/~2/metadata", 1)},
		{name: "copy from annotations disables all folds", body: "[" + patch + `,{"from":"/metadata/annotations","op":"copy","path":"/spec/test"},` + patch + "]"},
		{name: "move from annotations disables all folds", body: `[{"path":"/data/test","op":"move","from":"/metadata/annotations/` + pointerKey + `"},` + patch + "]"},
		{
			name:   "copy from a differently named segment leaves folding enabled",
			body:   "[" + patch + `,{"op":"copy","from":"/data/annotations~1key","path":"/data/test"}]`,
			want:   "[" + strings.Replace(patch, large, marker, 1) + `,{"op":"copy","from":"/data/annotations~1key","path":"/data/test"}]`,
			folded: 1,
		},
		{
			name:          "duplicate keys across nested objects in first repeat order",
			body:          `{"z":0,"array":[{"a":1,"\u0061":2,"a":3},{"a":4,"a":5}],"z":1,"only":0,"other":{"only":1}}`,
			duplicateKeys: []string{"a", "z"},
		},
		{
			name:          "duplicate patch path leaves value visible",
			body:          "[" + strings.Replace(patch, `"op":"replace"`, `"op":"replace","path":"/data/config"`, 1) + "]",
			duplicateKeys: []string{"path"},
		},
		{
			name:   "preserve escapes numbers and key order",
			body:   `{"z":"\u003c","metadata":{"annotations":{` + key + `:"` + escaped + `","short":"\u003c"}},"a":"\/","n":1e+09}` + " \r\n\n",
			want:   `{"z":"\u003c","metadata":{"annotations":{` + key + `:"<folded: 258 bytes>","short":"\u003c"}},"a":"\/","n":1e+09}`,
			folded: 1,
		},
		{name: "yaml is unchanged", body: yaml, unformatted: true},
		{name: "invalid json is unchanged", body: malformed, unformatted: true},
		{name: "trailing input leaves json unchanged", body: object + " true", unformatted: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			want := tt.want
			if want == "" {
				want = tt.body
			}
			if !tt.unformatted {
				var indented bytes.Buffer
				require.NoError(t, json.Indent(&indented, []byte(want), "", "  "))
				want = indented.String()
			}

			got := foldKubeBody(tt.body)

			assert.Equal(t, !tt.unformatted, got.formatted)
			assert.Equal(t, tt.folded, got.folded)
			assert.Equal(t, want, got.body)
			assert.Equal(t, tt.duplicateKeys, got.duplicateKeys)
		})
	}
}

func TestWriteKubeBody(t *testing.T) {
	t.Parallel()
	const body = "first\r\n\t\x1b[31msecond\x1b[0m"
	tests := []struct {
		name     string
		body     string
		terminal bool
		color    bool
		want     string
		warning  string
	}{
		{name: "terminal removes escapes and adds newline", body: body, terminal: true, want: "first\n\tsecond\n", warning: "Warning: " + scriptAlteredWarning + "\n"},
		{name: "terminal warning follows stderr color", body: body, terminal: true, color: true, want: "first\n\tsecond\n", warning: "\x1b[1m\x1b[33mWarning\x1b[0m: " + scriptAlteredWarning + "\n"},
		{name: "terminal preserves a final newline", body: "first\r\n", terminal: true, want: "first\n"},
		{name: "redirected output is unchanged", body: body, want: body},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ios, out, errOut := utils.TestIOStreams()
			ios.ColorEnabled = tt.color

			require.NoError(t, writeKubeBody(ios, tt.body, tt.terminal))

			assert.Equal(t, tt.want, out.String())
			assert.Equal(t, tt.warning, errOut.String())
		})
	}
}
