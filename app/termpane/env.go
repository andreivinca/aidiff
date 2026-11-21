package termpane

import "strings"

// termProgram is what the pane's terminal tells its programs it is
// (TERM_PROGRAM): the program hosting it, not the terminal that program
// itself runs in.
const termProgram = "aidiff"

// hostTerminalVars are the variables naming the terminal and multiplexer the
// host program runs in; a trailing "*" matches a prefix. A pane's shell
// inherits the host's environment, but it does not run there: with these
// set, tmux refuses to start ("sessions should be nested with care"), and
// tmux, zellij, kitty, wezterm, herdr, agterm and cmux commands typed in the
// pane, launch-revdiff.sh among them (it keys on these to pick an overlay),
// act on the host's pane instead. TERM, COLORTERM and TERM_PROGRAM are
// replaced by the pane's own (childEnv).
var hostTerminalVars = []string{
	"TERM", "COLORTERM", "TERM_PROGRAM", "TERM_PROGRAM_VERSION", "TERM_SESSION_ID",
	"TMUX", "TMUX_PANE",
	"ZELLIJ*",
	"HERDR_*",
	"HC_TERMINAL_ID", // Herdr.app (herdr-core)
	"AGTERM_*",
	"KITTY_*",
	"WEZTERM_*",
	"CMUX_*", "__CFBundleIdentifier",
	"GHOSTTY_*",
	"ITERM_*", "LC_TERMINAL", "LC_TERMINAL_VERSION",
	"INSIDE_EMACS",
	"ALACRITTY_*",
	"KONSOLE_*",
	"VTE_VERSION",
	"WT_SESSION", "WT_PROFILE_ID",
}

// childEnv is env, the host's environment, for a program in a pane: the
// host terminal's variables (hostTerminalVars) left out, the pane's own
// terminal described instead.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env)+3)
	for _, kv := range env {
		name, _, _ := strings.Cut(kv, "=")
		if !hostTerminalVar(name) {
			out = append(out, kv)
		}
	}
	return append(out, "TERM=xterm-256color", "COLORTERM=truecolor", "TERM_PROGRAM="+termProgram)
}

// hostTerminalVar reports whether the variable name is one of
// hostTerminalVars.
func hostTerminalVar(name string) bool {
	for _, v := range hostTerminalVars {
		if prefix, ok := strings.CutSuffix(v, "*"); (ok && strings.HasPrefix(name, prefix)) || v == name {
			return true
		}
	}
	return false
}
