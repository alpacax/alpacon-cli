package approval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"strings"

	approvalapi "github.com/alpacax/alpacon-cli/api/approval"
	"github.com/alpacax/alpacon-cli/utils"
)

const (
	foldAnnotationOver        = 256
	lastAppliedAnnotation     = "kubectl.kubernetes.io/last-applied-configuration"
	maxDuplicateKeyWarnings   = 5
	kubeBodyUnavailableReason = "The server includes it only for readers who can decide the request or open its work session, and clears it after a while"
)

func kubeBodyKind(isChange *bool) string {
	switch {
	case isChange == nil:
		return ""
	case *isChange:
		return "change set"
	default:
		return "whole object"
	}
}

func printKubeBody(ios *utils.IOStreams, id string, kr *approvalapi.KubeRequest) {
	_, _ = fmt.Fprintln(ios.Out)
	if kr.Body == nil {
		_, _ = fmt.Fprintf(ios.Out, "Body: not included in this response. %s.\n", kubeBodyUnavailableReason)
		return
	}

	result := foldKubeBody(*kr.Body)
	body, altered := sanitizeScript(result.body)
	if altered {
		warnAboutAlteredDescribeText(ios)
	}
	shown := min(len(result.duplicateKeys), maxDuplicateKeyWarnings)
	for _, key := range result.duplicateKeys[:shown] {
		_, _ = fmt.Fprintf(ios.Out, "Warning: the body repeats the key %q in one object; Kubernetes may apply only the last value.\n", utils.SanitizeTerminalOneLine(key))
	}
	if more := len(result.duplicateKeys) - shown; more > 0 {
		_, _ = fmt.Fprintf(ios.Out, "Warning: and %d more repeated keys.\n", more)
	}

	details := []string{kubeBodyKind(kr.IsChange), utils.SanitizeTerminalOneLine(kr.ContentType)}
	if result.formatted {
		details = append(details, "formatted")
	}
	if result.folded > 0 {
		details = append(details, "folded "+lastAppliedAnnotation)
	}
	details = append(details, fmt.Sprintf("%d bytes as sent", len(*kr.Body)))
	_, _ = fmt.Fprintf(ios.Out, "Body (%s):\n%s", joinNonEmpty(details, ", "), body)
	if !strings.HasSuffix(body, "\n") {
		_, _ = fmt.Fprintln(ios.Out)
	}
	if result.formatted {
		_, _ = fmt.Fprintf(ios.Out, "Run 'alpacon approval describe %s --body > FILE' to save the body exactly as sent.\n", utils.SanitizeTerminalOneLine(id))
	}
}

// writeKubeBody writes the original body on redirected stdout and sanitizes it on a terminal.
func writeKubeBody(ios *utils.IOStreams, body string, terminal bool) error {
	if terminal {
		var altered bool
		body, altered = sanitizeScript(body)
		if altered {
			ios.WarnTerminalTextAltered(ios.ErrOut, "")
		}
		if !strings.HasSuffix(body, "\n") {
			body += "\n"
		}
	}
	_, err := io.WriteString(ios.Out, body)
	return err
}

var lastAppliedPath = [...]string{"metadata", "annotations", lastAppliedAnnotation}

type annotationSpan struct {
	start, end int
	// prefixNeeded counts the path segments that must come from a patch pointer.
	prefixNeeded int
}

type kubeBodyResult struct {
	body          string
	folded        int
	formatted     bool
	duplicateKeys []string
}

type kubeBodyWalker struct {
	body           string
	dec            *json.Decoder
	spans          []annotationSpan
	duplicateKeys  []string
	reportedKeys   map[string]bool
	preventFolding bool
}

// foldKubeBody indents JSON, reports repeated keys, and folds eligible annotation strings in place.
func foldKubeBody(body string) kubeBodyResult {
	original := kubeBodyResult{body: body}
	w := kubeBodyWalker{
		body:         body,
		dec:          json.NewDecoder(strings.NewReader(body)),
		reportedKeys: make(map[string]bool),
	}
	w.dec.UseNumber()
	token, err := w.dec.Token()
	if err != nil {
		return original
	}
	// The root marker prevents a partial match outside a patch value.
	if err := w.walk(token, 0, []string{""}, true); err != nil {
		return original
	}

	if w.preventFolding {
		w.spans = nil
	}
	var replaced bytes.Buffer
	offset := 0
	for _, span := range w.spans {
		replaced.WriteString(body[offset:span.start])
		_, _ = fmt.Fprintf(&replaced, `"<folded: %d bytes>"`, span.end-span.start-2)
		offset = span.end
	}
	replaced.WriteString(body[offset:])
	var indented bytes.Buffer
	if err := json.Indent(&indented, replaced.Bytes(), "", "  "); err != nil {
		return original
	}
	return kubeBodyResult{
		body:          strings.TrimRight(indented.String(), " \t\r\n"),
		folded:        len(w.spans),
		formatted:     true,
		duplicateKeys: w.duplicateKeys,
	}
}

// walk visits a value; a nil path disables folding but still checks for duplicate keys.
func (w *kubeBodyWalker) walk(token json.Token, before int, path []string, root bool) error {
	switch token {
	case json.Delim('{'):
		return w.object(path, false)
	case json.Delim('['):
		if path != nil {
			// An array index cannot match any segment in the annotation path.
			path = append(path, "")
		}
		for w.dec.More() {
			before := int(w.dec.InputOffset())
			child, err := w.dec.Token()
			if err != nil {
				return err
			}
			if root && child == json.Delim('{') {
				err = w.object(nil, true)
			} else {
				err = w.walk(child, before, path, false)
			}
			if err != nil {
				return err
			}
		}
		_, err := w.dec.Token()
		return err
	default:
		if _, isString := token.(string); isString && path != nil {
			w.collectSpan(before, path)
		}
		return nil
	}
}

func (w *kubeBodyWalker) collectSpan(before int, path []string) {
	matched := min(len(path), len(lastAppliedPath))
	needed := len(lastAppliedPath) - matched
	if !slices.Equal(path[len(path)-matched:], lastAppliedPath[needed:]) {
		return
	}

	end := int(w.dec.InputOffset())
	// The offset before a value can include separators and whitespace.
	start := before + strings.IndexByte(w.body[before:end], '"')
	if end-start-2 > foldAnnotationOver {
		w.spans = append(w.spans, annotationSpan{start, end, needed})
	}
}

// object checks keys and resolves patch value spans after reading the full operation.
func (w *kubeBodyWalker) object(path []string, patchOp bool) error {
	seen := make(map[string]bool)
	firstSpan := len(w.spans)
	var pointer []string
	var copiesOrMoves, fromAnnotations, ambiguousValue bool
	for w.dec.More() {
		token, err := w.dec.Token()
		if err != nil {
			return err
		}
		key := token.(string)
		repeated := seen[key]
		if repeated && !w.reportedKeys[key] {
			w.reportedKeys[key] = true
			w.duplicateKeys = append(w.duplicateKeys, key)
		}
		seen[key] = true

		before := int(w.dec.InputOffset())
		value, err := w.dec.Token()
		if err != nil {
			return err
		}
		var valuePath []string
		if path != nil {
			valuePath = append(path, key)
		}
		if patchOp {
			valueText, isString := value.(string)
			switch key {
			case "path":
				if isString {
					pointer = jsonPointerSegments(valueText)
				}
				ambiguousValue = ambiguousValue || repeated
			case "value":
				valuePath = []string{}
				ambiguousValue = ambiguousValue || repeated
			case "op":
				copiesOrMoves = copiesOrMoves || valueText == "copy" || valueText == "move"
			case "from":
				fromAnnotations = fromAnnotations || slices.Contains(jsonPointerSegments(valueText), "annotations")
			}
		}
		if err := w.walk(value, before, valuePath, false); err != nil {
			return err
		}
	}
	if _, err := w.dec.Token(); err != nil {
		return err
	}
	if patchOp {
		w.preventFolding = w.preventFolding || copiesOrMoves && fromAnnotations
		// Filter in place to keep spans in the order the decoder visited them.
		kept := slices.DeleteFunc(w.spans[firstSpan:], func(span annotationSpan) bool {
			needed := span.prefixNeeded
			return ambiguousValue || pointer == nil || len(pointer) < needed ||
				!slices.Equal(pointer[len(pointer)-needed:], lastAppliedPath[:needed])
		})
		w.spans = w.spans[:firstSpan+len(kept)]
	}
	return nil
}

var pointerUnescaper = strings.NewReplacer("~1", "/", "~0", "~")

func jsonPointerSegments(pointer string) []string {
	if pointer == "" {
		return []string{}
	}
	path, valid := strings.CutPrefix(pointer, "/")
	if !valid {
		return nil
	}
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		for rest := segment; ; {
			_, after, found := strings.Cut(rest, "~")
			if !found {
				break
			}
			if after == "" || (after[0] != '0' && after[0] != '1') {
				return nil
			}
			rest = after[1:]
		}
		segments[i] = pointerUnescaper.Replace(segment)
	}
	return segments
}
