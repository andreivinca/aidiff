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

type storeModel struct {
	tea.Model
	store *annotation.Store
}

func (m storeModel) Store() *annotation.Store { return m.store }

func flashText(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd)
	msg, ok := cmd().(splitview.FlashMsg)
	require.True(t, ok)
	return msg.Text
}

func TestCopyAnnotationsCommand(t *testing.T) {
	cb := &fakeClipboard{}
	c := copyAnnotationsCommand(cb)
	assert.Equal(t, "Copy annotations", c.Name)

	empty := storeModel{store: annotation.NewStore()}
	assert.Equal(t, "no annotations to copy", flashText(t, c.Run(empty)))
	assert.Empty(t, cb.text, "nothing is written")

	store := annotation.NewStore()
	store.Add(annotation.Annotation{File: "a.go", Line: 3, Type: "+", Comment: "check this"})
	assert.Equal(t, "1 annotation(s) copied", flashText(t, c.Run(storeModel{store: store})))
	assert.Equal(t, store.FormatOutput(), cb.text, "the output format, as q or O writes it")
	assert.Contains(t, cb.text, "## a.go:3 (+)")

	cb.err = errors.New("no clipboard tool")
	assert.Equal(t, "copy annotations: no clipboard tool", flashText(t, c.Run(storeModel{store: store})))
}
