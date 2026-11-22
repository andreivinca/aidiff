package chat

import (
	"context"
	"errors"
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

// escapeLocked dismisses a command popup, then stops files going up with a
// message, before offering to stop the agent.
func (p *Pane) escapeLocked() {
	if len(p.suggestionsLocked()) > 0 {
		p.sugHidden = true
		return
	}
	if p.stopSendLocked(errSendCancelled) {
		p.note = "cancelling the upload…"
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
// was chosen and the turn still runs. The draft and its attachments stay:
// stopping is independent of composing.
func (p *Pane) confirmStopLocked() {
	choice := p.stop
	p.stop = stopConfirmation{}
	if !choice.stop || !p.turnRunningLocked(choice.turn) {
		return
	}
	b := p.backend
	err := p.requestLocked(request{
		run: func(ctx context.Context) error {
			err := b.Interrupt(ctx, choice.turn)
			if err != nil && !errors.Is(err, ErrStopped) {
				return fmt.Errorf("stop failed: %w", err)
			}
			return err
		},
	})
	if err != nil {
		p.note = err.Error()
	}
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
