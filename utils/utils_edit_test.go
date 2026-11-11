package utils

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	osexec "os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type failingEditFile struct {
	*os.File
	writeErr error
	closeErr error
}

func (f *failingEditFile) Write(data []byte) (int, error) {
	if f.writeErr != nil {
		return 0, f.writeErr
	}
	return f.File.Write(data)
}

func (f *failingEditFile) Close() error {
	if err := f.File.Close(); err != nil {
		return err
	}
	return f.closeErr
}

// recordingEditor stands in for the user's editor and records the path it was handed.
type recordingEditor struct {
	path string
	edit func(path string) error
}

func (e *recordingEditor) run(path string) error {
	e.path = path
	if e.edit == nil {
		return nil
	}
	return e.edit(path)
}

func writeEdit(content string) func(path string) error {
	return func(path string) error { return os.WriteFile(path, []byte(content), 0o600) }
}

func TestEditTempFileRemovesFileWhenWritingOrClosingFails(t *testing.T) {
	t.Parallel()
	writeErr := errors.New("write failed")
	closeErr := errors.New("close failed")
	tests := []struct {
		name     string
		writeErr error
		closeErr error
		wantErr  error
	}{
		{name: "write failure", writeErr: writeErr, wantErr: writeErr},
		{name: "close failure", closeErr: closeErr, wantErr: closeErr},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			file, err := os.CreateTemp(t.TempDir(), "record.*.json")
			require.NoError(t, err)
			t.Cleanup(func() { _ = file.Close() })
			editor := &recordingEditor{}
			failingFile := &failingEditFile{File: file, writeErr: tt.writeErr, closeErr: tt.closeErr}

			data, err := editTempFile(failingFile, []byte(`{"name":"my-webhook"}`), editor.run)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, data)
			assert.Empty(t, editor.path)
			assert.NoFileExists(t, file.Name())
		})
	}
}

func TestProcessEditedDataReturnsEditedJSONAndRemovesTempFile(t *testing.T) {
	t.Parallel()
	original := []byte(`{"name":"my-webhook","enabled":true}`)
	pretty := "{\n\t\"name\": \"my-webhook\",\n\t\"enabled\": true\n}"
	tests := []struct {
		name    string
		content string
		want    any
	}{
		{
			name:    "edited field",
			content: `{"name":"my-webhook","enabled":false}`,
			want:    map[string]any{"name": "my-webhook", "enabled": false},
		},
		{
			name:    "whitespace only edit still counts as a change",
			content: pretty + "\n",
			want:    map[string]any{"name": "my-webhook", "enabled": true},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			editor := &recordingEditor{edit: writeEdit(tt.content)}

			got, err := processEditedData(original, editor.run)

			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
			require.NotEmpty(t, editor.path)
			assert.NoFileExists(t, editor.path)
		})
	}
}

func TestProcessEditedDataReturnsErrorAndRemovesTempFile(t *testing.T) {
	t.Parallel()
	editorFailed := errors.New("editor exited with status 1")
	tests := []struct {
		name    string
		edit    func(path string) error
		wantErr error
	}{
		{name: "unchanged file", edit: nil, wantErr: ErrNoChanges},
		{name: "failed editor", edit: func(string) error { return editorFailed }, wantErr: editorFailed},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			editor := &recordingEditor{edit: tt.edit}

			got, err := processEditedData([]byte(`{"name":"my-webhook"}`), editor.run)

			require.ErrorIs(t, err, tt.wantErr)
			assert.Nil(t, got)
			require.NotEmpty(t, editor.path)
			assert.NoFileExists(t, editor.path)
		})
	}
}

func TestProcessEditedDataRejectsInvalidJSON(t *testing.T) {
	t.Parallel()
	editor := &recordingEditor{edit: writeEdit("{")}

	got, err := processEditedData([]byte(`{"name":"my-webhook"}`), editor.run)

	var syntaxErr *json.SyntaxError
	require.ErrorAs(t, err, &syntaxErr)
	assert.Nil(t, got)
	assert.NoFileExists(t, editor.path)
}

func TestProcessEditedDataHandsTheEditorPrettyJSON(t *testing.T) {
	t.Parallel()
	var content []byte
	editor := &recordingEditor{edit: func(path string) error {
		var err error
		content, err = os.ReadFile(path)
		return err
	}}

	_, err := processEditedData([]byte(`{"name":"my-webhook"}`), editor.run)

	require.ErrorIs(t, err, ErrNoChanges)
	want := []byte("{\n\t\"name\": \"my-webhook\"\n}")
	assert.Equal(t, want, content, "the editor gets tab-indented JSON, not just equivalent JSON")
}

func TestExitIfNoChangesReturnsOnOtherErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
	}{
		{"nil error", nil},
		{"unrelated error", errors.New("editor exited with status 1")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ExitIfNoChanges(tt.err)
		})
	}
}

func TestExitIfNoChangesPrintsTheNoticeAndExitsZero(t *testing.T) {
	tests := []struct {
		name   string
		format string
	}{
		{"table output", OutputFormatTable},
		{"json output prints no envelope", OutputFormatJSON},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			help := osexec.Command(os.Args[0], "-test.run=^TestExitIfNoChangesHelperProcess$")
			help.Env = append(os.Environ(), "GO_WANT_NO_CHANGES_HELPER="+tt.format)
			var stdout, stderr bytes.Buffer
			help.Stdout = &stdout
			help.Stderr = &stderr

			err := help.Run()

			require.NoError(t, err)
			assert.Empty(t, stdout.String())
			assert.Equal(t, "Info: No changes made. Aborting update.\n", stderr.String())
		})
	}
}

func TestExitIfNoChangesHelperProcess(t *testing.T) {
	format := os.Getenv("GO_WANT_NO_CHANGES_HELPER")
	if format == "" {
		return
	}
	OutputFormat = format
	ExitIfNoChanges(fmt.Errorf("failed to update: %w", ErrNoChanges))
	_, _ = fmt.Fprintln(os.Stderr, "ExitIfNoChanges returned")
}

func TestPendingSignalReturnsASignalBufferedBeforeDone(t *testing.T) {
	t.Parallel()
	for range 100 { // select picks a ready case at random, so one pass could miss a dropped signal
		sigs := make(chan os.Signal, 1)
		sigs <- os.Interrupt
		done := make(chan struct{})
		close(done)

		got := pendingSignal(sigs, done)

		require.Equal(t, os.Interrupt, got)
	}
}

func TestPendingSignalReturnsNilWhenDoneWithNoSignal(t *testing.T) {
	t.Parallel()
	done := make(chan struct{})
	close(done)

	got := pendingSignal(make(chan os.Signal, 1), done)

	assert.Nil(t, got)
}

func TestReportNoChanges_AnswersOnlyForNoChanges(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"no changes", ErrNoChanges, true},
		{"wrapped no changes", fmt.Errorf("failed to update: %w", ErrNoChanges), true},
		{"another failure", errors.New("server refused the edit"), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := ReportNoChanges(tt.err)

			assert.Equal(t, tt.want, got)
		})
	}
}
