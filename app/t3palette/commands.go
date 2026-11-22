package t3palette

import (
	"cmp"
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
)

// callTimeout bounds one palette operation on a connected T3 server.
const callTimeout = 30 * time.Second

// threadsGroup is the palette section the thread rows are listed in.
const threadsGroup = "Threads"

// inlineThreads is how many recent threads the palette lists directly.
const inlineThreads = 8

// Commands lists the T3 entries for the palette's current state: on this
// machine's connection, Add Project… and connecting to a server; else a new
// thread first, then the live server's recent threads, then what the thread
// on screen offers.
func (t *Palette) Commands() []splitview.Command {
	t.refreshShell()
	v := t.state()
	if v.live == nil {
		return []splitview.Command{t.addProjectCmd(), {Name: "T3: Connect to server…", Run: t.connectPicker}}
	}
	server := v.live.Label()
	scope := server
	if v.project != nil {
		scope = v.project.Title
	}
	cmds := []splitview.Command{
		{Name: "T3: New thread…", Detail: scope, Run: t.newThread},
		{Name: "T3: Add Project…", Detail: server, Run: t.addT3Project},
	}
	cmds = append(cmds, t.threadCommands(v, scope)...)
	if v.onScreen != nil {
		cmds = append(cmds, splitview.Command{Name: "T3: Diff View…", Detail: cmp.Or(v.onScreen.sourceLabel(), t.cfg.DiffLabel), Run: t.diffCmd})
	}
	// Send annotations to agent is in the toolbar's ⋯ menu (SendAnnotations)
	return append(cmds,
		splitview.Command{Name: "T3: Connect to another server…", Run: t.connectPicker})
}

// threadCommands are the rows of the live server's most recent threads in
// the selected project, the one on screen marked, and a row listing them all
// when there are more.
func (t *Palette) threadCommands(v state, scope string) []splitview.Command {
	threads := projectThreads(v.shell, v.project)
	current := v.currentThreadID()
	cmds := make([]splitview.Command, 0, inlineThreads+1)
	for _, th := range threads[:min(len(threads), inlineThreads)] {
		label := "T3: " + threadTitle(th)
		if th.ID == current {
			label = "T3: ▸ " + threadTitle(th)
		}
		cmds = append(cmds, splitview.Command{Name: label, Detail: threadDetail(v.shell, th), Group: threadsGroup,
			Run: t.openThread(v.live, th.ID)})
	}
	if len(threads) > inlineThreads {
		cmds = append(cmds, splitview.Command{Name: "T3: All threads…", Detail: scope, Group: threadsGroup, Run: t.threadPicker})
	}
	return cmds
}

// currentThreadID is the id of the thread on screen when it is one of the
// live server's, else "".
func (v state) currentThreadID() string {
	if v.onScreen == nil || v.onScreen.Session != v.live {
		return ""
	}
	if th := v.onScreen.thread(); th != nil {
		return th.ID
	}
	return ""
}

// onLive runs cmd for the live session, or offers to connect when there is
// none yet.
func (t *Palette) onLive(cmd func(s Session) tea.Cmd) tea.Cmd {
	s := t.liveSession()
	if s == nil {
		return t.connectPicker(nil)
	}
	return cmd(s)
}

// ask is a command that runs call with a context bounded by callTimeout and
// reports with the message call returns.
func ask(call func(ctx context.Context) tea.Msg) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), callTimeout)
		defer cancel()
		return call(ctx)
	}
}

// asking is ask behind a flash that says what is under way.
func asking(what string, call func(ctx context.Context) tea.Msg) tea.Cmd {
	return tea.Batch(flash("T3: "+what+"…"), ask(call))
}

// flash is a command that shows text on the bottom row.
func flash(text string) tea.Cmd {
	return func() tea.Msg {
		return splitview.FlashMsg{Text: text}
	}
}

// t3Error reports a failed T3 step on the bottom row.
func t3Error(what string, err error) tea.Msg {
	return splitview.FlashMsg{Text: "T3: " + what + ": " + err.Error()}
}

// t3Failed reports a failure whose error names the failed step.
func t3Failed(err error) tea.Msg {
	return splitview.FlashMsg{Text: "T3: " + err.Error()}
}

// withNotices follows msg with a flash of s's notices, when it has any.
func withNotices(s Session, msg tea.Msg) tea.Msg {
	notices := s.Notices()
	if len(notices) == 0 {
		return msg
	}
	first := func() tea.Msg {
		return msg
	}
	return tea.Sequence(first, flash("T3: "+strings.Join(notices, "; ")))()
}
