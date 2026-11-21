package splitview

import (
	"reflect"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// A terminal reports no key release in its usual encodings, so on its own
// the session switcher cannot tell when the user lets go of ctrl. The kitty
// keyboard protocol can: pushed onto the terminal's stack of keyboard
// flags, "report event types" sends presses, repeats and releases, and
// "report all keys as escape codes" includes the modifier keys themselves.
// Bubble Tea v1 names none of these reports, so the flags are on only while
// the switcher is open, which takes every key itself (switcherKeyReport):
// pushed as it opens, popped as it closes, and always one for one.
//
// The protocol keeps one stack for the main screen and one for the
// alternate screen, which the program runs on, so the pop has to come
// before the program leaves it: before a quit, a suspend or an external
// program takes the terminal (filterQuit), never after Run returns, when it
// would pop an entry of the main screen's that the shell may own.
//
// A terminal that does not speak the protocol ignores all of it: no answer
// to the query, no releases, and the switcher waits for enter.

const (
	keyReportFlags  = kittyDisambiguate | kittyEventTypes | kittyAllKeys
	kittyAnswered   = kittyEventTypes | kittyAllKeys // the flags releases of modifier keys need
	pushKeyReports  = "\x1b[>11u"                    // CSI > keyReportFlags u
	popKeyReports   = "\x1b[<u"                      // pops one entry
	queryKeyReports = "\x1b[?u"                      // answered "CSI ? flags u"
)

// The kitty protocol's progressive enhancement flags the split uses.
const (
	kittyDisambiguate = 1
	kittyEventTypes   = 2
	kittyAllKeys      = 8
)

// keyReports is the terminal's keyboard mode as the split set it. Every
// copy of the Model shares it: the terminal is one. The split uses it on
// the UI goroutine only (Update, and the program's filter, which Bubble Tea
// runs there too).
type keyReports struct {
	write  func(seq string) // Config.Output; nil writes nothing
	pushed bool             // the switcher's flags are on the terminal's stack
	flags  int              // what the terminal last said is in effect after a push; -1 before any answer
}

// newKeyReports starts with nothing pushed and nothing known.
func newKeyReports(write func(string)) *keyReports {
	return &keyReports{write: write, flags: -1}
}

// follow pushes the switcher's flags while it is open and pops them once it
// closes; anything else changes nothing.
func (k *keyReports) follow(open bool) {
	if open {
		k.push()
		return
	}
	k.pop()
}

// push puts the switcher's flags on the terminal's stack, and asks which
// flags took effect while that is not known yet.
func (k *keyReports) push() {
	if k == nil || k.write == nil || k.pushed {
		return
	}
	k.write(pushKeyReports)
	if k.flags < 0 {
		k.write(queryKeyReports)
	}
	k.pushed = true
}

// pop takes the switcher's flags back off the terminal's stack. It does
// nothing when they are not on it, so every way out may call it.
func (k *keyReports) pop() {
	if k == nil || !k.pushed {
		return
	}
	k.write(popKeyReports)
	k.pushed = false
}

// answered records the flags the terminal said are in effect.
func (k *keyReports) answered(flags int) {
	if k != nil {
		k.flags = flags
	}
}

// releases reports whether the terminal reports the release of modifier
// keys while the switcher's flags are on.
func (k *keyReports) releases() bool {
	return k != nil && k.flags >= 0 && k.flags&kittyAnswered == kittyAnswered
}

// active reports whether releases are being reported now.
func (k *keyReports) active() bool {
	return k.releases() && k.pushed
}

// keyFlagsReply reads the terminal's answer to queryKeyReports,
// "CSI ? flags u". It is never a key, whenever it comes.
func keyFlagsReply(seq []byte) (int, bool) {
	s, ok := strings.CutPrefix(string(seq), "\x1b[?")
	if !ok {
		return 0, false
	}
	s, ok = strings.CutSuffix(s, "u")
	if !ok {
		return 0, false
	}
	flags, err := strconv.Atoi(s)
	if err != nil || flags < 0 {
		return 0, false
	}
	return flags, true
}

// teaExecMsg is the name of the message Bubble Tea runs an external program
// with (tea.ExecProcess), handing it the terminal; unexported, so it is
// matched by name, as rawSequence matches its unknown-sequence message.
// TestHandsOverTerminal fails when an upgrade renames it.
const teaExecMsg = "execMsg"

// handsOverTerminal reports whether Bubble Tea gives the terminal away on
// msg, leaving the alternate screen: to quit, to suspend, or to run another
// program.
func handsOverTerminal(msg tea.Msg) bool {
	switch msg.(type) {
	case tea.QuitMsg, tea.SuspendMsg, tea.InterruptMsg:
		return true
	}
	t := reflect.TypeOf(msg)
	return t != nil && t.PkgPath() == teaPkgPath && t.Name() == teaExecMsg
}
