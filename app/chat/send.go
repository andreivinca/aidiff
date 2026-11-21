package chat

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

var (
	errNotConnected = errors.New(linkOffline)
	errBusy         = errors.New("a request is still being sent; try again in a moment")
)

// submitLocked sends the input to the agent, or runs its slash command. The
// draft is cleared once the message or command took it; when it was turned
// down, the draft stays and the note says why. mu must be held.
func (p *Pane) submitLocked() {
	text := strings.TrimSpace(p.input.text())
	if text == "" {
		if q := p.activeChoiceLocked(); q != nil && len(q.Options) > 0 {
			if p.questionChoice >= len(q.Options) {
				p.note = "Type your own answer below, then press Enter"
				return
			}
			if err := p.answerCmd(fmt.Sprint(p.questionChoice + 1)); err != nil {
				p.note = err.Error()
			}
			return
		}
		if prompt := p.pendingQuestionsLocked(); prompt != nil && len(prompt.Questions) > 1 && p.answersFor == prompt.ID && p.questionTab == len(prompt.Questions) {
			if err := p.submitAnswersLocked(prompt); err != nil {
				p.note = err.Error()
			}
		}
		return
	}
	var err error
	if strings.HasPrefix(text, "/") {
		err = p.commandLocked(text)
	} else if p.pendingQuestionsLocked() != nil {
		err = p.answerCmd(text)
	} else {
		err = p.sendMessageLocked(text)
	}
	if err != nil {
		p.note = err.Error()
		return
	}
	p.clearDraftLocked()
}

// clearDraftLocked empties the input along with the images pasted into it;
// mu must be held.
func (p *Pane) clearDraftLocked() {
	p.input.clear()
	p.images = nil
	p.inputChanged()
}

// sendMessageLocked hands text and the images it names to the agent. Its
// card shows above the composer until the backend takes the message over.
// mu must be held.
func (p *Pane) sendMessageLocked(text string) error {
	pasted := p.imagesInLocked(text)
	m := Outgoing{ID: newID(), Text: text}
	for _, img := range pasted {
		m.Images = append(m.Images, img.image)
	}
	b := p.backend
	err := p.requestLocked(request{
		run: func(ctx context.Context) error {
			if err := b.Send(ctx, m); err != nil {
				return fmt.Errorf("send failed: %w", err)
			}
			return nil
		},
		finish: func(err error) {
			p.sentLocked(m, err)
		},
		keep: func(err error) {
			if err != nil {
				p.restoreLocked(m.Text, pasted)
			}
		},
	})
	if err != nil {
		return err
	}
	p.pending = append(p.pending, queuedMessage{id: m.ID, text: text, sending: true, queued: p.runningTurnLocked() != ""})
	p.scroll = 0
	return nil
}

// sentLocked settles a message's card once the backend answered; the card
// of one that failed goes, its text back in the input (restoreLocked). mu
// must be held.
func (p *Pane) sentLocked(m Outgoing, err error) {
	for i := range p.pending {
		if p.pending[i].id == m.ID {
			p.pending[i].sending = false
		}
	}
	if err != nil {
		p.removePendingLocked(m.ID)
	}
}

// restoreLocked puts text the backend did not take back into the input,
// with the images it names: in place of an empty draft, else ahead of what
// was typed since, on a line of its own, so neither is lost. mu must be
// held.
func (p *Pane) restoreLocked(text string, pasted []pendingImage) {
	p.images = append(p.images, pasted...)
	for _, img := range pasted {
		p.input.addAtom(img.token)
	}
	if p.input.empty() {
		p.input.insert(text)
		p.inputChanged()
		return
	}
	cur := p.input.cur
	p.input.moveTo(0, false)
	p.input.insert(text + "\n")
	p.input.moveTo(cur+p.input.cur, false) // the cursor stays where it was in the draft
	p.inputChanged()
}

// request is a call to the backend the composer waits on; one runs at a
// time.
type request struct {
	run    func(ctx context.Context) error // off the lock; its error says what failed
	done   string                          // the note once it succeeded, if any
	finish func(err error)                 // with mu held once run returned, before the note; may be nil
	// keep, with mu held once run returned, gives the user back text the
	// backend did not take. It runs even when the pane follows another
	// backend by then, unlike finish: that text belongs to the draft, which
	// outlives the conversation (Follow). May be nil.
	keep func(err error)
}

// requestLocked starts r, unless the pane is not connected or waits on
// another request. mu must be held.
func (p *Pane) requestLocked(r request) error {
	if p.backend == nil {
		return errNotConnected
	}
	if p.busy {
		return errBusy
	}
	p.busy = true
	sub := p.subID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), requestTimeout)
		defer cancel()
		err := r.run(ctx)
		p.lock()
		defer p.mu.Unlock()
		if r.keep != nil {
			r.keep(err)
		}
		if sub == p.subID {
			p.busy = false
			p.settleLocked(r, err)
		} else if err != nil && r.keep != nil {
			p.note = err.Error() // what came back into the input says why
		}
		p.changedLocked()
	}()
	return nil
}

// settleLocked reports how a request went; mu must be held.
func (p *Pane) settleLocked(r request, err error) {
	if r.finish != nil {
		r.finish(err)
	}
	switch {
	case err != nil:
		p.note = err.Error()
	case r.done != "":
		p.note = r.done
	}
}

// replyLocked carries out what a backend's command left to do: a note
// now, or a request with the note once it succeeded. mu must be held.
func (p *Pane) replyLocked(r Reply) error {
	if r.Send == nil {
		if r.Note != "" {
			p.note = r.Note
		}
		return nil
	}
	return p.requestLocked(request{run: r.Send, done: r.Note})
}
