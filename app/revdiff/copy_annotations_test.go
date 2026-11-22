package main

import (
	"context"
	"errors"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/splitview"
)

type fakeClipboard struct {
	text string
	err  error
}

func (c *fakeClipboard) Write(_ context.Context, text string) error {
	c.text = text
	return c.err
}

func flashText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd)
	msg, ok := cmd().(splitview.FlashMsg)
	require.True(t, ok)
	return msg.Text
}

func TestCopyAnnotations(t *testing.T) {
	cb := &fakeClipboard{}
	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 3, Type: "+", Comment: "check this"})
	assert.Equal(t, "1 annotation(s) copied", flashText(t, copyAnnotations(cb)(store.FormatOutput(), 1)))
	assert.Equal(t, store.FormatOutput(), cb.text, "the output format, as q or O writes it")
	assert.Contains(t, cb.text, "## a.go:3 (+)")

	cb.err = errors.New("no clipboard tool")
	assert.Equal(t, "copy annotations: no clipboard tool", flashText(t, copyAnnotations(cb)(store.FormatOutput(), 1)))
}
