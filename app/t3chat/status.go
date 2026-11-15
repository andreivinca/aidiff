package t3chat

import (
	"fmt"
	"strings"
)

type activityCounts struct {
	valid             bool
	agents, processes int
}

// Counts are recomputed only on activity changes, never on each keystroke.
// The same ID reconciliation as the activity view avoids counting a process
// twice when its launch command also has a pending background-task record.
func (p *Pane) activeCountsLocked() (int, int) {
	if !p.activityCounts.valid {
		counts := activityCounts{valid: true}
		for _, entry := range p.activityEntriesLocked() {
			if !entry.active() {
				continue
			}
			if entry.kind == "subagent" {
				counts.agents++
			} else {
				counts.processes++
			}
		}
		p.activityCounts = counts
	}
	return p.activityCounts.agents, p.activityCounts.processes
}

func (p *Pane) activityStatusLocked() string {
	if p.link != "" {
		return p.link
	}
	agents, processes := p.activeCountsLocked()
	state := p.stateLocked()
	if state == "" {
		state = "idle"
	}
	if state == "running" || (state == "idle" && agents+processes > 0) {
		state = "working"
	}
	state = strings.ToUpper(state[:1]) + state[1:]
	if agents+processes == 0 {
		return state
	}
	processLabel := "processes"
	if processes == 1 {
		processLabel = "process"
	}
	return fmt.Sprintf("%s · %d subagent%s · %d %s", state, agents, plural(agents), processes, processLabel)
}
