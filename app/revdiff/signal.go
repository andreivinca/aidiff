package main

import (
	"os"
	"os/signal"
	"sync"
	"sync/atomic"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
)

//go:generate moq -out quitter_moq_test.go -pkg main -skip-ensure -fmt goimports . quitter

// quitter is the consumer-side interface for *tea.Program's Quit, so the
// signal-to-quit wiring stays unit-testable with a mock.
type quitter interface{ Quit() }

// shutdownGuard turns a terminating OS signal (SIGHUP/SIGTERM) into a single
// graceful Quit and records that the exit was signal-driven. SIGINT is caught
// and drained without quitting (see watch/handle). The flag is read only after
// p.Run() joins, so the annotation store is never touched off the main goroutine.
//
// With a deadline, the first signal also starts it, so a quit that never
// completes still ends the process, and any later signal ends it at once.
type shutdownGuard struct {
	once     sync.Once
	signaled atomic.Bool
	deadline *exitDeadline // nil: signals only quit
}

func (g *shutdownGuard) trigger(quit func()) {
	first := false
	g.once.Do(func() {
		first = true
		g.signaled.Store(true)
		g.deadline.arm(shutdownLimit)
		quit()
	})
	if !first {
		g.deadline.expireNow()
	}
}

func (g *shutdownGuard) handle(ch <-chan os.Signal, quit func()) {
	for sig := range ch {
		if sig == syscall.SIGINT {
			// caught-and-ignored: a Ctrl-C meant for an external $EDITOR must not
			// quit revdiff; Notify keeps SIGINT from default-terminating the process.
			continue
		}
		g.trigger(quit)
	}
}

func (g *shutdownGuard) watch(q quitter) (stop func()) {
	ch := make(chan os.Signal, 1)
	// SIGHUP/SIGTERM trigger the graceful quit; SIGINT is registered too but
	// drained by handle without quitting. registering SIGINT via Notify (not
	// signal.Ignore) prevents its default terminate disposition — a SIGINT
	// delivered while an external $EDITOR owns the terminal via tea.ExecProcess
	// arrives in cooked mode as a Ctrl-C the user meant for the editor and must
	// not hard-kill revdiff. mirrors bubbletea's ignoreSignals-during-Exec, which
	// WithoutSignalHandler disables. a typed ^C inside the raw-mode TUI is already
	// a keystroke, not a signal. signal.Stop in the stop func restores the default
	// disposition for all three, so a signal during a hung finalize can kill the
	// process (signal.Reset does NOT undo signal.Ignore, which is why we avoid it).
	signal.Notify(ch, syscall.SIGHUP, syscall.SIGTERM, syscall.SIGINT)
	go g.handle(ch, q.Quit)
	// idempotent: run() calls stop() explicitly before finalize and via defer.
	var once sync.Once
	return func() {
		once.Do(func() {
			signal.Stop(ch)
			close(ch)
		})
	}
}

// forceQuitter quits the split without its quit dialog. A signal-driven quit
// must not ask: after a hangup the terminal is gone, nobody can answer, and the
// process would keep running unseen, holding the T3 SSH tunnel and its port
// forwards.
type forceQuitter struct{ p *tea.Program }

// Quit does not wait for the program to take the message, so a wedged event
// loop cannot block the signal handler and a second signal still gets through.
func (f forceQuitter) Quit() {
	go f.p.Send(splitview.ForceQuitMsg{})
}

func (g *shutdownGuard) wasSignaled() bool {
	return g.signaled.Load()
}
