package t3chat

import (
	"strings"

	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"

	"github.com/umputun/revdiff/app/t3client"
)

type stopConfirmation struct {
	runID string // stop only the turn the user was asked about
	stop  bool   // false selects Keep working by default
}

// modalTextLocked gives confirmations and Activity first refusal of a key.
func (p *Pane) modalTextLocked(s string) bool {
	if p.stop.runID != "" {
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
	if p.activeRunLocked() != "" {
		p.stopCmd("")
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

func (p *Pane) confirmStopLocked() {
	choice := p.stop
	p.stop = stopConfirmation{}
	if !choice.stop || !p.runs[choice.runID].Active() {
		return
	}
	if p.busy {
		p.note = "a request is still being sent; try stopping again in a moment"
		return
	}
	if p.conn == nil {
		p.note = "not connected"
		return
	}
	p.busy = true
	// Keep the draft and attachments: stopping is independent of composing.
	go p.dispatch(p.conn, t3client.InterruptCommand(p.threadID, choice.runID), "stop")
}

func (p *Pane) renderStopLocked(w, h int) string {
	inner := max(1, min(56, w-4))
	title, message := "Stop agent?", "Interrupt the current turn? Your draft will be kept."
	if !p.runs[p.stop.runID].Active() {
		title, message = "This turn has finished", "Nothing will be interrupted."
	}
	lines := []string{"", "  " + p.styles.bold.Render(title), ""}
	for line := range strings.SplitSeq(ansi.Wrap(message, max(1, inner-4), ""), "\n") {
		lines = append(lines, "  "+line)
	}
	stop, keep := "  Stop agent  ", "  Keep working  "
	if p.stop.stop {
		stop = p.styles.bad.Render("[ Stop agent ]")
	} else {
		keep = p.styles.rail.Render("[ Keep working ]")
	}
	lines = append(lines, "", "  "+stop)
	if ansi.StringWidth(stop+" "+keep)+2 <= inner {
		lines[len(lines)-1] += " " + keep
	} else {
		lines = append(lines, "  "+keep)
	}
	lines = append(lines, "", "  ←→ choose · enter confirm · esc keep working", "")
	rows := make([]string, 0, h)
	for range max(0, (h-len(lines))/2) {
		rows = append(rows, strings.Repeat(" ", w))
	}
	left := strings.Repeat(" ", max(0, (w-inner)/2))
	for _, line := range lines {
		if len(rows) == h {
			break
		}
		rows = append(rows, fit(left+p.styles.composerLine(fit(line, inner)), w))
	}
	for len(rows) < h {
		rows = append(rows, strings.Repeat(" ", w))
	}
	return strings.Join(rows, "\n")
}
