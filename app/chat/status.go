package chat

import (
	"fmt"
	"unicode"
	"unicode/utf8"
)

// activityCounts are the subagents and processes still going, counted once
// per conversation the backend shows, never on a keystroke or render.
type activityCounts struct {
	agents, processes int
}

// countActivity counts the active entries of the activity list.
func countActivity(entries []Activity) activityCounts {
	var counts activityCounts
	for _, entry := range entries {
		if !entry.active() {
			continue
		}
		if entry.Agent {
			counts.agents++
		} else {
			counts.processes++
		}
	}
	return counts
}

// activityStatusLocked is the status line: the connection's problem, or
// what the agent is doing with how much work is still going. Work going on
// in the background keeps an idle agent working. mu must be held.
func (p *Pane) activityStatusLocked() string {
	if p.link != "" {
		return p.link
	}
	agents, processes := p.counts.agents, p.counts.processes
	agent := p.agentStateLocked()
	state := string(agent)
	if agent == AgentRunning || (agent == AgentIdle && agents+processes > 0) {
		state = "working"
	}
	state = capitalize(state)
	if agents+processes == 0 {
		return state
	}
	return fmt.Sprintf("%s · %d subagent%s · %d %s", state, agents, plural(agents), processes, processWord(processes))
}

// capitalize upper-cases the first letter of s.
func capitalize(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	return string(unicode.ToUpper(r)) + s[size:]
}

// processWord is "process" or "processes", for n of them.
func processWord(n int) string {
	if n == 1 {
		return "process"
	}
	return "processes"
}

// plural is the "s" of n things, "" for one.
func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
