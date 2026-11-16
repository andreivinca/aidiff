package chat

import (
	"context"
	"fmt"
	"slices"
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type stopConfirmation struct {
	turn string // stop only the turn the user was asked about
	stop bool   // false selects Keep working by default
}

// modalTextLocked gives confirmations and Activity first refusal of a key.
func (p *Pane) modalTextLocked(s string) bool {
	if p.stop.turn != "" {
		return p.stopTextLocked(s)
	}
	return p.activity.open && p.activityTextLocked(s)
}

// escapeLocked dismisses a command popup before offering to stop the agent.
func (p *Pane) escapeLocked() {
	if len(p.suggestionsLocked()) > 0 {
		p.sugHidden = true
		return
	}
	if turn := p.runningTurnLocked(); turn != "" {
		p.stop = stopConfirmation{turn: turn}
	}
}

func (p *Pane) stopTextLocked(s string) bool {
	switch s {
	case "\x1b":
		p.stop = stopConfirmation{}
	case "\t":
		p.stop.stop = !p.stop.stop
	case "\r":
		p.confirmStopLocked()
	default:
		p.stop = stopConfirmation{}
		return false // typing goes back to the draft; letters never approve a stop
	}
	return true
}

func (p *Pane) stopKeyLocked(k uv.KeyPressEvent) {
	switch k.Code {
	case uv.KeyLeft:
		p.stop.stop = true
	case uv.KeyRight:
		p.stop.stop = false
	default:
		// Navigation outside the dialog's buttons cannot edit the draft.
	}
}

// confirmStopLocked interrupts the turn the dialog asked about, when Stop
// was chosen and the turn still runs. The draft and its images stay:
// stopping is independent of composing.
func (p *Pane) confirmStopLocked() {
	choice := p.stop
	p.stop = stopConfirmation{}
	if !choice.stop || !p.turnRunningLocked(choice.turn) {
		return
	}
	b := p.backend
	queued := len(p.conv.Queued) > 0
	err := p.requestLocked(request{
		run: func(ctx context.Context) error {
			if err := b.Interrupt(ctx, choice.turn); err != nil {
				return fmt.Errorf("stop failed: %w", err)
			}
			return nil
		},
		finish: func(err error) {
			if err == nil && (queued || len(p.conv.Queued) > 0) {
				p.offerResumeLocked()
			}
		},
	})
	if err != nil {
		p.note = err.Error()
	}
}

// resumeCommand is the backend command that sends the messages a stop held
// back in the queue.
const resumeCommand = "/resume"

// offerResumeLocked puts the backend's /resume in an empty input after a
// stop that held queued messages back, so enter sends them. It is one
// character, which one delete removes whole. A draft stays
// as it is, and the note says what to type. Nothing happens for a backend
// without /resume. mu must be held.
func (p *Pane) offerResumeLocked() {
	if p.backend == nil || !slices.ContainsFunc(p.backend.Commands(), func(c Command) bool {
		return c.Name == resumeCommand
	}) {
		return
	}
	if !p.input.empty() {
		p.note = "stopped; the queued messages wait: " + resumeCommand + " sends them"
		return
	}
	p.input.insertAtom(resumeCommand)
	p.inputChanged()
	p.note = "stopped; enter sends the queued messages"
}

// turnRunningLocked reports whether the turn is still going; mu must be
// held.
func (p *Pane) turnRunningLocked(turn string) bool {
	return slices.Contains(p.conv.Running, turn)
}

// stopRowsLocked draws the stop dialog in the middle of the pane; mu must be
// held.
func (p *Pane) stopRowsLocked(w, h int) []string {
	inner := max(1, min(56, w-4))
	lines := p.stopDialogLocked(inner)
	rows := make([]string, 0, h)
	for range max(0, (h-len(lines))/2) {
		rows = append(rows, strings.Repeat(" ", w))
	}
	left := strings.Repeat(" ", max(0, (w-inner)/2))
	for _, line := range lines {
		if len(rows) == h {
			break
		}
		rows = append(rows, fit(left+p.composer.fill(line, inner), w))
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", w))
	}
	return rows
}

// stopDialogLocked is the dialog's lines for a box inner cells wide, drawn
// with the composer palette; mu must be held.
func (p *Pane) stopDialogLocked(inner int) []string {
	c := p.composer
	title, message := "Stop agent?", "Interrupt the current turn? Your draft will be kept."
	if !p.turnRunningLocked(p.stop.turn) {
		title, message = "This turn has finished", "Nothing will be interrupted."
	}
	indent := c.text.Render("  ")
	lines := []string{"", indent + c.bold.Render(title), ""}
	for line := range strings.SplitSeq(ansi.Wrap(message, max(1, inner-4), ""), "\n") {
		lines = append(lines, c.text.Render("  "+line))
	}
	stop, keep := c.text.Render("  Stop agent  "), c.text.Render("  Keep working  ")
	if p.stop.stop {
		stop = c.bad.Render("[ Stop agent ]")
	} else {
		keep = c.rail.Render("[ Keep working ]")
	}
	lines = append(lines, "", indent+stop)
	if ansi.StringWidth(stop+" "+keep)+2 <= inner {
		lines[len(lines)-1] += c.text.Render(" ") + keep
	} else {
		lines = append(lines, indent+keep)
	}
	return append(lines, "", c.text.Render("  ←→ choose · enter confirm · esc keep working"), "")
}
