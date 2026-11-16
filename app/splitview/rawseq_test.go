package splitview

import (
	"io"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// msgRecorder is a root model that reports every message it receives.
type msgRecorder struct {
	got chan<- tea.Msg
}

func (r msgRecorder) Init() tea.Cmd {
	return nil
}

func (r msgRecorder) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	r.got <- msg
	return r, nil
}

func (r msgRecorder) View() string {
	return ""
}

// unknownCSIMsg types seq into a real Bubble Tea program and returns the
// message Bubble Tea makes of it.
func unknownCSIMsg(t *testing.T, seq string) tea.Msg {
	t.Helper()
	got := make(chan tea.Msg, 16)
	p := tea.NewProgram(msgRecorder{got: got}, tea.WithInput(strings.NewReader(seq)), tea.WithOutput(io.Discard),
		tea.WithoutRenderer(), tea.WithoutSignalHandler())
	go func() {
		_, _ = p.Run()
	}()
	defer p.Kill()
	select {
	case msg := <-got:
		return msg
	case <-time.After(testTimeout):
		t.Fatal("Bubble Tea delivered no message")
		return nil
	}
}

func TestRawSequence_BubbleTeaType(t *testing.T) {
	// fails when a Bubble Tea upgrade renames or reshapes the unexported
	// message rawSequence depends on (see rawseq.go)
	msg := unknownCSIMsg(t, "\x1b[13;2u")
	seq, ok := rawSequence(msg)
	require.True(t, ok, "Bubble Tea's message for an unknown CSI sequence is recognized: %T", msg)
	assert.Equal(t, []byte("\x1b[13;2u"), seq)

	// ctrl+delete has no Bubble Tea name either: it reaches a chat pane raw
	seq, ok = rawSequence(unknownCSIMsg(t, "\x1b[3;5~"))
	require.True(t, ok)
	assert.Equal(t, []byte("\x1b[3;5~"), seq)

	_, ok = rawSequence(tea.KeyMsg{Type: tea.KeyEnter})
	assert.False(t, ok, "named keys are not raw sequences")
	_, ok = rawSequence(nil)
	assert.False(t, ok)
}
