package chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// command is a slash command: one of the pane's own, which run sets, or a
// backend's, carried out by its Run.
type command struct {
	Command
	run func(arg string) error // the pane's own, with mu held; nil for a backend's
}

// agent reports a command of the agent's own, which goes to it as a
// message.
func (c command) agent() bool {
	return c.run == nil && c.Run == nil
}

// builtinCommands are the pane's own slash commands, in the order the popup
// lists them.
func (p *Pane) builtinCommands() []command {
	return []command{
		{Command: Command{Name: "/help", Description: "how the commands work"}, run: p.helpCmd},
		{Command: Command{Name: "/stop", Description: "confirm stopping the running turn"}, run: p.stopCmd},
		{Command: Command{Name: "/accept", Aliases: []string{"/yes", "/y"}, Description: "approve what the agent asked for"},
			run: p.decider(Accept)},
		{Command: Command{Name: "/decline", Aliases: []string{"/no", "/n"}, Description: "decline what the agent asked for"},
			run: p.decider(Decline)},
		{Command: Command{Name: "/cancel", Description: "cancel the agent's request"}, run: p.decider(Cancel)},
		{Command: Command{Name: "/answer", Args: "<n> | 1,3 | <text>", Description: "answer the agent's question (/1…/9 pick an option)"},
			run: p.answerCmd},
		{Command: Command{Name: "/dismiss", Description: "drop the agent's question"}, run: p.dismissCmd},
		{Command: Command{Name: "/attach", Args: "<file>", Description: "attach a file or an image (ctrl+v pastes copied files or an image)"},
			run: p.attachCmd},
		{Command: Command{Name: "/work", Description: "show or hide the agent's commands, edits and thoughts one by one"},
			run: p.workCmd},
		{Command: Command{Name: "/activity", Description: "show the agent's live subagents and processes (← on an empty input)"},
			run: p.activityCmd},
	}
}

// closeCommand is /close, for a pane that can be closed from inside it
// (Config.Closable).
func (p *Pane) closeCommand() command {
	return command{Command: Command{Name: "/close", Description: "close this side chat; its thread stays on the server"}, run: p.closeCmd}
}

// closeCmd is /close: the pane closes, as the × on its header does.
func (p *Pane) closeCmd(string) error {
	p.closeSelfLocked()
	return nil
}

// commandsLocked lists the pane's commands, then the backend's; mu must be
// held.
func (p *Pane) commandsLocked() []command {
	out := slices.Clone(p.builtins)
	if p.backend != nil {
		for _, c := range p.backend.Commands() {
			out = append(out, command{Command: c})
		}
	}
	return out
}

// findCommandLocked matches a typed word to a command by name or alias,
// the pane's own first. The agent's commands are not matched: they go to
// it as messages, as any unknown command does. mu must be held.
func (p *Pane) findCommandLocked(word string) (command, bool) {
	for _, c := range p.commandsLocked() {
		if c.agent() {
			continue
		}
		if word == c.Name || slices.Contains(c.Aliases, word) {
			return c, true
		}
	}
	return command{}, false
}

// commandLocked runs the slash command typed in the input. One neither the
// pane nor the backend knows goes to the agent as a message; mu must be
// held.
func (p *Pane) commandLocked(text string) error {
	fields := strings.Fields(text)
	word := strings.ToLower(fields[0])
	arg := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	if n, err := strconv.Atoi(word[1:]); err == nil && n > 0 {
		return p.answerCmd(word[1:]) // /2 is /answer 2
	}
	c, ok := p.findCommandLocked(word)
	switch {
	case !ok:
		return p.sendMessageLocked(text)
	case c.run != nil:
		return c.run(arg)
	}
	reply, err := c.Run(arg)
	if err != nil {
		return err
	}
	return p.replyLocked(reply)
}

func (p *Pane) helpCmd(string) error {
	p.note = "type / to see every command, the agent's included; ↑↓ pick one, tab completes it, enter runs it, esc hides the list"
	return nil
}

func (p *Pane) stopCmd(string) error {
	turn := p.runningTurnLocked()
	if turn == "" {
		return errors.New("nothing is running")
	}
	p.stop = stopConfirmation{turn: turn}
	return nil
}

// decider answers the newest pending approval with the decision.
func (p *Pane) decider(d Decision) func(string) error {
	return func(string) error {
		id := p.pendingApprovalLocked()
		if id == "" {
			return errors.New("nothing is waiting for an approval")
		}
		b := p.backend
		return p.requestLocked(request{run: func(ctx context.Context) error {
			if err := b.Decide(ctx, id, d); err != nil {
				return fmt.Errorf("answer failed: %w", err)
			}
			return nil
		}})
	}
}

// pickedApprovalLocked is the approval the picker's keys answer: the newest
// pending one, unless the agent's questions hold the keys. mu must be held.
func (p *Pane) pickedApprovalLocked() string {
	if p.pendingQuestionsLocked() != nil {
		return ""
	}
	return p.pendingApprovalLocked()
}

// pendingApprovalLocked is the newest approval the agent still waits on, or
// ""; mu must be held.
func (p *Pane) pendingApprovalLocked() string {
	for _, e := range slices.Backward(p.conv.Entries) {
		if a, ok := e.(Approval); ok && a.Pending && a.ID != "" {
			return a.ID
		}
	}
	return ""
}

// attachCmd attaches a file by path: an image as one, any other file to
// be read when the message is sent.
func (p *Pane) attachCmd(arg string) error {
	path := attachArg(arg)
	if path == "" {
		return errors.New("/attach <file>; ctrl+v pastes copied files or an image from the clipboard")
	}
	p.attachPathsLocked([]string{path}, "")
	return nil
}

// workCmd toggles between the quiet summary of the agent's work and every
// step.
func (p *Pane) workCmd(string) error {
	p.showWork = !p.showWork
	if p.showWork {
		p.note = "showing every step; /work hides them again"
	} else {
		p.note = "work is summarized; /work shows every step"
	}
	return nil
}

// activityCmd opens the activity view.
func (p *Pane) activityCmd(string) error {
	p.activity.open = true
	return nil
}
