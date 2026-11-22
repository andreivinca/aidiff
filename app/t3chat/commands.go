package t3chat

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/umputun/revdiff/app/chat"
	"github.com/umputun/revdiff/app/t3client"
)

// runtimeModes are T3's permission levels, with what each allows.
var runtimeModes = []chat.Suggestion{
	{Insert: "/mode approval-required", Label: "approval-required", Description: "ask before every command and edit", Runs: true},
	{Insert: "/mode auto-accept-edits", Label: "auto-accept-edits", Description: "edit files freely, ask before commands", Runs: true},
	{Insert: "/mode auto", Label: "auto", Description: "act freely, ask for the risky", Runs: true},
	{Insert: "/mode full-access", Label: "full-access", Description: "never ask", Runs: true},
}

// ownCommands are T3's slash commands, in the order the popup lists them.
func (b *Backend) ownCommands() []chat.Command {
	return []chat.Command{
		{Name: "/model", Args: "<model>", Description: "switch the thread's model", Run: b.modelCmd, Complete: b.modelSuggestions},
		{Name: "/effort", Args: "[level]", OptionalArg: true, Description: "show or change reasoning effort",
			Run: b.effortCmd, Complete: b.effortSuggestions},
		{Name: "/plan", Description: "plan mode: the agent proposes before it acts", Run: b.interactionCmd("plan")},
		{Name: "/build", Aliases: []string{"/default"}, Description: "build mode: the agent acts", Run: b.interactionCmd("default")},
		{Name: "/mode", Args: "<runtime-mode>", Description: "what the agent may do on its own", Run: b.modeCmd, Complete: b.modeSuggestions},
		{Name: "/clear", Description: b.clearDescription(), Run: b.clearCmd},
		{Name: "/side", Aliases: []string{"/btw"}, Args: "<question>", Description: "ask a side question in a new side chat", Run: b.sideCmd},
	}
}

// clearDescription says what /clear does here.
func (b *Backend) clearDescription() string {
	if b.newThread != nil {
		return "start over in a new thread of this project"
	}
	return "start a fresh session with the next message; earlier messages stay"
}

// Commands are T3's slash commands, then the commands the thread's
// provider understands, which go to the agent as messages. The pane asks
// on every frame while a slash command is typed, so they are read without
// mu, which the connection's reader holds while it builds a conversation:
// T3's own are fixed once the backend is handed out, and the provider is
// the one showLocked saw last (instanceLocked).
func (b *Backend) Commands() []chat.Command {
	b.instanceMu.Lock()
	instance := b.instance
	b.instanceMu.Unlock()
	out := slices.Clone(b.commands)
	for _, pc := range b.providerCommands(instance) {
		out = append(out, providerCommand(pc))
	}
	return out
}

// providerCommands are the catalog's commands for the provider instance,
// or every provider's while it is not known ("").
func (b *Backend) providerCommands(instance string) []t3client.ProviderCommand {
	cat, _ := b.catalog.get()
	if cat == nil {
		return nil
	}
	if instance == "" {
		return cat.Commands
	}
	var out []t3client.ProviderCommand
	for _, c := range cat.Commands {
		if c.InstanceID == instance {
			out = append(out, c)
		}
	}
	return out
}

// instanceLocked keeps the provider instance of the selection the next
// message runs with, for Commands; mu must be held.
func (b *Backend) instanceLocked() {
	instance := ""
	if m := b.selectionLocked(); m != nil {
		instance = m.InstanceID
	}
	b.instanceMu.Lock()
	b.instance = instance
	b.instanceMu.Unlock()
}

// providerCommand is a provider's command as the popup lists it.
func providerCommand(pc t3client.ProviderCommand) chat.Command {
	c := chat.Command{Name: "/" + pc.Name, Description: cmp.Or(pc.Description, pc.Hint, "run the agent's "+pc.Name+" command")}
	if pc.Hint != "" {
		c.Args = pc.Hint
		if !strings.HasPrefix(c.Args, "<") {
			c.Args = "<" + c.Args + ">"
		}
	}
	return c
}

// batch is commands sent one after another.
type batch struct {
	srv  server
	what string // names the batch in a failure
	cmds []t3client.Command
}

// send dispatches the commands in order, stopping at the first failure.
func (b batch) send(ctx context.Context) error {
	for _, cmd := range b.cmds {
		if err := b.srv.Dispatch(ctx, cmd); err != nil {
			return fmt.Errorf("%s failed: %w", b.what, err)
		}
	}
	return nil
}

// dispatch is the reply that sends cmds; what names them in a failure.
func (b *Backend) dispatch(what string, cmds ...t3client.Command) chat.Reply {
	return chat.Reply{Send: batch{srv: b.srv, what: what, cmds: cmds}.send}
}

// interactionCmd switches between plan and build mode.
func (b *Backend) interactionCmd(mode string) func(string) (chat.Reply, error) {
	return func(string) (chat.Reply, error) {
		return b.dispatch(mode+" mode", t3client.InteractionModeCommand(b.threadID, mode)), nil
	}
}

func (b *Backend) modeCmd(arg string) (chat.Reply, error) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	for _, m := range runtimeModes {
		if m.Label == arg {
			return b.dispatch("mode", t3client.RuntimeModeCommand(b.threadID, arg)), nil
		}
	}
	return chat.Reply{}, errors.New("/mode approval-required | auto-accept-edits | auto | full-access")
}

func (b *Backend) modeSuggestions(prefix string) []chat.Suggestion {
	var out []chat.Suggestion
	for _, m := range runtimeModes {
		if strings.HasPrefix(m.Label, strings.ToLower(prefix)) {
			out = append(out, m)
		}
	}
	return out
}

// clearCmd starts over in a new thread of the same project, with this
// thread's model and modes, and switches the screen and the chat to it.
// Without a way to switch threads it arms a fresh provider session for the
// next message instead, like T3's /clear: the transcript stays, the agent's
// context does not.
func (b *Backend) clearCmd(string) (chat.Reply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.newThread != nil {
		b.newThread(NewThread{From: b.threadID, Model: b.selectionLocked(), RuntimeMode: b.thread.runtimeMode,
			InteractionMode: b.thread.interaction})
		return chat.Reply{Note: "starting a new thread…"}, nil
	}
	b.fresh = true
	b.showLocked()
	return chat.Reply{Note: "the next message starts a fresh session; the transcript stays"}, nil
}

// sideCmd forks the thread into a side chat and asks the question there.
func (b *Backend) sideCmd(arg string) (chat.Reply, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	newID := t3client.NewID()
	cmds := []t3client.Command{t3client.SideChatCommand(b.threadID, newID)}
	if q := strings.TrimSpace(arg); q != "" {
		msg := t3client.OutgoingMessage{ThreadID: newID, MessageID: t3client.NewID(), Text: q, Model: b.selectionLocked()}
		cmds = append(cmds, msg.Command())
	}
	reply := b.dispatch("side chat", cmds...)
	reply.Note = "side chat started; ctrl+p lists it with the other threads"
	if open := b.sideChat; open != nil {
		side, send := SideChat{From: b.threadID, ID: newID}, reply.Send
		reply.Send = func(ctx context.Context) error {
			if err := send(ctx); err != nil {
				return err
			}
			open(side) // the fork went through: the host shows it
			return nil
		}
		reply.Note = "side chat started; it opens beside this one"
	}
	return reply, nil
}
