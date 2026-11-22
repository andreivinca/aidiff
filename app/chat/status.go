package chat

import (
	"fmt"
	"time"
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
	if state == "Working" && !p.conv.RunningSince.IsZero() {
		state += " " + elapsedText(p.now().Sub(p.conv.RunningSince))
		p.tickLocked()
	}
	if agents+processes == 0 {
		return state
	}
	return fmt.Sprintf("%s · %d subagent%s · %d %s", state, agents, plural(agents), processes, processWord(processes))
}

// workingLocked reports whether the agent is at work on a turn. mu must be
// held.
func (p *Pane) workingLocked() bool {
	return len(p.conv.Running) > 0 || p.agentStateLocked() == AgentRunning
}

// clockTick is how often the status line's elapsed time is redrawn.
var clockTick = time.Second

// tickLocked asks for a redraw on the next whole second of the elapsed
// time, so it counts on while nothing else changes; one timer at a time,
// and none once the pane is closed. mu must be held.
func (p *Pane) tickLocked() {
	if p.ticking || p.closed {
		return
	}
	p.ticking = true
	wait := clockTick
	if clockTick == time.Second && !p.conv.RunningSince.IsZero() {
		wait = time.Second - p.now().Sub(p.conv.RunningSince)%time.Second
	}
	time.AfterFunc(wait, func() {
		p.mu.Lock()
		defer p.mu.Unlock()
		p.ticking = false
		if !p.closed {
			p.changedLocked()
		}
	})
}

// elapsedText is a duration the way the status line counts it: 12s,
// 5m 24s, 1h 03m.
func elapsedText(d time.Duration) string {
	d = max(d, 0)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d/time.Second))
	case d < time.Hour:
		return fmt.Sprintf("%dm %02ds", int(d/time.Minute), int(d%time.Minute/time.Second))
	}
	return fmt.Sprintf("%dh %02dm", int(d/time.Hour), int(d%time.Hour/time.Minute))
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
