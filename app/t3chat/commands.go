package t3chat

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/umputun/revdiff/app/t3client"
)

// maxSuggestionRows bounds the popup under the transcript.
const maxSuggestionRows = 6

// command is one slash command the pane handles itself. Commands the
// provider handles come from the server's catalog and are sent as messages.
type command struct {
	name    string
	aliases []string
	args    string // argument placeholder shown in the popup; "" when the command takes none
	desc    string
	run     func(p *Pane, arg string) // with mu held
	// complete lists argument suggestions for what was typed after the name;
	// nil when the command takes none or any text.
	complete func(p *Pane, prefix string) []suggestion
}

// suggestion is one row of the popup: what completion puts in the input and
// how the row reads.
type suggestion struct {
	insert string // the input text after completion; "" for a row that cannot be picked
	label  string
	desc   string
	runs   bool // enter runs it at once: a command without arguments, or a filled-in argument
	rank   int  // how well it matched; lower first
}

// commands lists the pane's slash commands in the order the popup shows them.
var commands = []command{
	{name: "/help", desc: "how the commands work", run: (*Pane).helpCmd},
	{name: "/stop", desc: "confirm stopping the running turn", run: (*Pane).stopCmd},
	{name: "/accept", aliases: []string{"/yes", "/y"}, desc: "approve what the agent asked for", run: decide("accept")},
	{name: "/decline", aliases: []string{"/no", "/n"}, desc: "decline what the agent asked for", run: decide("decline")},
	{name: "/cancel", desc: "cancel the agent's request", run: decide("cancel")},
	{name: "/answer", args: "<n> | 1,3 | <text>", desc: "answer the agent's question (/1…/9 pick an option)", run: (*Pane).answerLocked},
	{name: "/dismiss", desc: "drop the agent's question", run: (*Pane).dismissCmd},
	{name: "/model", args: "<model>", desc: "switch the thread's model", run: (*Pane).modelCmd, complete: (*Pane).modelSuggestions},
	{name: "/plan", desc: "plan mode: the agent proposes before it acts", run: setInteraction("plan")},
	{name: "/build", aliases: []string{"/default"}, desc: "build mode: the agent acts", run: setInteraction("default")},
	{name: "/mode", args: "<runtime-mode>", desc: "what the agent may do on its own", run: (*Pane).modeCmd, complete: (*Pane).modeSuggestions},
	{name: "/clear", desc: "start a fresh session with the next message; earlier messages stay", run: (*Pane).clearCmd},
	{name: "/side", aliases: []string{"/btw"}, args: "<question>", desc: "ask a side question in a new side chat", run: (*Pane).sideCmd},
	{name: "/attach", args: "<image file>", desc: "attach an image file (ctrl+v pastes one from the clipboard)", run: (*Pane).attachCmd},
	{name: "/work", desc: "show or hide the agent's commands, edits and thoughts one by one", run: (*Pane).workCmd},
	{name: "/activity", desc: "show T3's live subagents and processes (← on an empty input)", run: (*Pane).activityCmd},
}

// runtimeModes are T3's permission levels, with what each allows.
var runtimeModes = []suggestion{
	{insert: "/mode approval-required", label: "approval-required", desc: "ask before every command and edit", runs: true},
	{insert: "/mode auto-accept-edits", label: "auto-accept-edits", desc: "edit files freely, ask before commands", runs: true},
	{insert: "/mode auto", label: "auto", desc: "act freely, ask for the risky", runs: true},
	{insert: "/mode full-access", label: "full-access", desc: "never ask", runs: true},
}

// findCommand matches a typed word to a command by name or alias.
func findCommand(word string) *command {
	for i := range commands {
		c := &commands[i]
		if word == c.name || slices.Contains(c.aliases, word) {
			return c
		}
	}
	return nil
}

// commandLocked runs the slash command typed in the input. One the pane
// does not know goes to the agent as a message, the way T3's composer sends
// its provider's commands; mu must be held.
func (p *Pane) commandLocked(text string) {
	fields := strings.Fields(text)
	word := strings.ToLower(fields[0])
	raw := strings.TrimSpace(strings.TrimPrefix(text, fields[0]))
	p.input.clear() // a command is consumed even when it only leaves a note
	p.inputChanged()
	if n, err := strconv.Atoi(word[1:]); err == nil && n > 0 {
		p.answerLocked(word[1:]) // /2 is /answer 2
		return
	}
	c := findCommand(word)
	if c == nil {
		p.sendMessageLocked(text)
		return
	}
	c.run(p, raw)
}

// suggestionsLocked lists what the typed slash text could become: commands
// for a bare word, then a command's argument choices; mu must be held.
func (p *Pane) suggestionsLocked() []suggestion {
	text := p.input.text()
	if p.sugHidden || !strings.HasPrefix(text, "/") || strings.Contains(text, "\n") {
		return nil
	}
	word, rest, hasArg := strings.Cut(text, " ")
	if !hasArg {
		return p.commandSuggestionsLocked(strings.ToLower(word))
	}
	c := findCommand(strings.ToLower(word))
	if c == nil || c.complete == nil {
		return nil
	}
	return c.complete(p, strings.TrimSpace(rest))
}

// commandSuggestionsLocked lists the pane's and the provider's commands a
// typed prefix could mean, best matches first; mu must be held.
func (p *Pane) commandSuggestionsLocked(prefix string) []suggestion {
	var out []suggestion
	for _, c := range commands {
		rank := rankMatch(c.name, c.aliases, c.desc, prefix)
		if rank < 0 {
			continue
		}
		s := suggestion{insert: c.name, label: c.name, desc: c.desc, runs: c.args == "", rank: rank}
		if c.args != "" {
			s.insert = c.name + " "
			s.label = c.name + " " + c.args
		}
		out = append(out, s)
	}
	p.ensureCatalogLocked()
	for _, pc := range p.providerCommandsLocked() {
		name := "/" + pc.Name
		if findCommand(name) != nil {
			continue // the pane's own command of that name wins, as T3's does
		}
		desc := pc.Description
		if desc == "" {
			desc = pc.Hint
		}
		if desc == "" {
			desc = "run the agent's " + pc.Name + " command"
		}
		rank := rankMatch(name, nil, desc, prefix)
		if rank < 0 {
			continue
		}
		s := suggestion{insert: name, label: name, desc: desc, runs: pc.Hint == "", rank: rank + 10}
		if pc.Hint != "" {
			hint := pc.Hint
			if !strings.HasPrefix(hint, "<") {
				hint = "<" + hint + ">"
			}
			s.insert = name + " "
			s.label = name + " " + hint
		}
		out = append(out, s)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].rank < out[j].rank })
	return out
}

// rankMatch scores how a typed prefix fits a command: the name exactly (0),
// at the start (1), an alias at the start (2), anywhere past the slash (3),
// in the description (4); -1 when it does not.
func rankMatch(name string, aliases []string, desc, prefix string) int {
	switch {
	case prefix == name:
		return 0
	case strings.HasPrefix(name, prefix):
		return 1
	}
	for _, a := range aliases {
		if strings.HasPrefix(a, prefix) {
			return 2
		}
	}
	q := strings.TrimPrefix(prefix, "/")
	switch {
	case q == "":
		return 1
	case strings.Contains(name, q):
		return 3
	case len(q) >= 3 && strings.Contains(strings.ToLower(desc), q):
		return 4 // a description match needs a few letters, or "st" would mean "request"
	}
	return -1
}

// providerCommandsLocked are the catalog's commands for the thread's
// provider, or every provider's while the thread's is unknown; mu must be held.
func (p *Pane) providerCommandsLocked() []t3client.ProviderCommand {
	if p.catalog == nil {
		return nil
	}
	if p.current == nil || p.current.InstanceID == "" {
		return p.catalog.Commands
	}
	var out []t3client.ProviderCommand
	for _, c := range p.catalog.Commands {
		if c.InstanceID == p.current.InstanceID {
			out = append(out, c)
		}
	}
	return out
}

// applySuggestionLocked completes the input with the selected suggestion.
// With enter it also runs a completed command. It reports whether a
// suggestion took the key; mu must be held.
func (p *Pane) applySuggestionLocked(enter bool) bool {
	sugs := p.suggestionsLocked()
	if len(sugs) == 0 {
		return false
	}
	typed := strings.TrimSpace(p.input.text())
	for _, s := range sugs {
		if s.insert != "" && strings.TrimSpace(s.insert) == typed {
			if enter {
				p.submitLocked() // what was typed is exactly a choice: run it
			}
			return true
		}
	}
	if word, _, hasArg := strings.Cut(typed, " "); enter && !hasArg {
		if c := findCommand(strings.ToLower(word)); c != nil && c.args == "" {
			p.submitLocked() // an exact command or alias runs, whatever else matches
			return true
		}
	}
	s := sugs[min(p.sug, len(sugs)-1)]
	if s.insert == "" {
		return false
	}
	p.input.clear()
	p.input.insert(s.insert)
	p.inputChanged()
	if enter && s.runs {
		p.submitLocked()
	}
	return true
}

// exactFirst moves the suggestion whose label is exactly what was typed to
// the top, so enter picks it over longer matches.
func exactFirst(sugs []suggestion, typed string) []suggestion {
	typed = strings.TrimSpace(typed)
	for i, s := range sugs {
		if i > 0 && strings.EqualFold(s.label, typed) {
			return append([]suggestion{s}, append(sugs[:i:i], sugs[i+1:]...)...)
		}
	}
	return sugs
}

// moveSuggestionLocked moves the popup's selection by delta, wrapping; it
// reports whether there was a popup; mu must be held.
func (p *Pane) moveSuggestionLocked(delta int) bool {
	n := len(p.suggestionsLocked())
	if n == 0 {
		return false
	}
	p.sug = ((p.sug+delta)%n + n) % n
	return true
}

// inputChanged resets the popup after an edit; mu must be held.
func (p *Pane) inputChanged() {
	p.sug = 0
	p.sugHidden = false
}

func (p *Pane) helpCmd(string) {
	p.note = "type / to see every command, the agent's included; ↑↓ pick one, tab completes it, enter runs it, esc hides the list"
}

func (p *Pane) stopCmd(string) {
	runID := p.activeRunLocked()
	if runID == "" {
		p.note = "nothing is running"
		return
	}
	p.stop = stopConfirmation{runID: runID}
}

// decide answers the pending approval with the decision.
func decide(decision string) func(*Pane, string) {
	return func(p *Pane, _ string) {
		requestID := p.pendingApprovalLocked()
		if requestID == "" {
			p.note = "nothing is waiting for an approval"
			return
		}
		p.dispatchLocked(t3client.RespondCommand(p.threadID, requestID, decision), "answer")
	}
}

func (p *Pane) dismissCmd(string) {
	item := p.pendingQuestionLocked()
	if item == nil {
		p.note = "the agent is not asking a question"
		return
	}
	p.answers, p.answersFor = nil, ""
	p.dispatchLocked(t3client.DismissQuestionCommand(p.threadID, item.RequestID), "dismiss")
}

// setInteraction switches between plan and build mode.
func setInteraction(mode string) func(*Pane, string) {
	return func(p *Pane, _ string) {
		p.dispatchLocked(t3client.InteractionModeCommand(p.threadID, mode), mode+" mode")
	}
}

func (p *Pane) modeCmd(arg string) {
	arg = strings.ToLower(strings.TrimSpace(arg))
	for _, m := range runtimeModes {
		if m.label == arg {
			p.dispatchLocked(t3client.RuntimeModeCommand(p.threadID, arg), "mode")
			return
		}
	}
	p.note = "/mode approval-required | auto-accept-edits | auto | full-access"
}

func (p *Pane) modeSuggestions(prefix string) []suggestion {
	var out []suggestion
	for _, m := range runtimeModes {
		if strings.HasPrefix(m.label, strings.ToLower(prefix)) {
			out = append(out, m)
		}
	}
	return exactFirst(out, prefix)
}

// clearCmd arms a fresh provider session for the next message, like T3's
// /clear: the transcript stays, the agent's context does not.
func (p *Pane) clearCmd(string) {
	p.fresh = true
	p.note = "the next message starts a fresh session; the transcript stays"
}

// sideCmd forks the thread into a side chat and asks the question there.
func (p *Pane) sideCmd(arg string) {
	if p.conn == nil {
		p.note = "not connected"
		return
	}
	newID := t3client.NewThreadID()
	cmds := []t3client.Command{t3client.SideChatCommand(p.threadID, newID)}
	if q := strings.TrimSpace(arg); q != "" {
		msg := t3client.MessageCommand(newID, q)
		if p.current != nil {
			msg["modelSelection"] = *p.current
		}
		cmds = append(cmds, msg)
	}
	p.busy = true
	p.input.clear()
	go p.dispatchSeq(p.conn, cmds, "side chat", "side chat started; it is listed under T3: Switch thread…")
}

// modelCmd switches the model, or reports the current one.
func (p *Pane) modelCmd(arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		p.note = "model: " + p.currentModelLocked() + " · /model <name> switches it"
		return
	}
	p.ensureCatalogLocked()
	if p.catalog == nil {
		p.note = "the model list is still loading; try again in a moment"
		return
	}
	choice, err := pickModel(p.catalog.Models, arg)
	if err != nil {
		p.note = err.Error()
		return
	}
	p.dispatchLocked(t3client.ModelSelectionCommand(p.threadID, choice.Selection()), "model")
}

// pickModel finds the model arg names: a slug, "instance:slug", or a unique
// part of a slug or name.
func pickModel(models []t3client.ModelChoice, arg string) (t3client.ModelChoice, error) {
	q := strings.ToLower(arg)
	var partial []t3client.ModelChoice
	for _, m := range models {
		slug := strings.ToLower(m.Slug)
		if q == slug || q == strings.ToLower(m.InstanceID)+":"+slug {
			return m, nil
		}
		if strings.Contains(slug, q) || strings.Contains(strings.ToLower(m.Name), q) {
			partial = append(partial, m)
		}
	}
	switch len(partial) {
	case 0:
		return t3client.ModelChoice{}, errors.New("no model matches " + arg + "; /model and tab lists them")
	case 1:
		return partial[0], nil
	}
	return t3client.ModelChoice{}, fmt.Errorf("%d models match %s; be more specific", len(partial), arg)
}

// modelSuggestions lists the server's models matching the typed text.
func (p *Pane) modelSuggestions(prefix string) []suggestion {
	p.ensureCatalogLocked()
	if p.catalog == nil {
		return []suggestion{{label: "loading the models…"}}
	}
	q := strings.ToLower(prefix)
	var out []suggestion
	for _, m := range p.catalog.Models {
		if q != "" && !strings.Contains(strings.ToLower(m.Slug+" "+m.Name+" "+m.Provider), q) {
			continue
		}
		desc := m.Name + " · " + m.Provider
		if p.current != nil && p.current.Model == m.Slug && p.current.InstanceID == m.InstanceID {
			desc += " · current"
		}
		out = append(out, suggestion{insert: "/model " + m.Slug, label: m.Slug, desc: desc, runs: true})
	}
	if len(out) == 0 {
		return []suggestion{{label: "no model matches " + prefix}}
	}
	return exactFirst(out, prefix)
}

// currentModelLocked names the thread's model; mu must be held.
func (p *Pane) currentModelLocked() string {
	if p.current == nil || p.current.Model == "" {
		return "unknown"
	}
	if p.catalog != nil {
		for _, m := range p.catalog.Models {
			if m.Slug == p.current.Model && m.InstanceID == p.current.InstanceID {
				return m.Slug + " (" + m.Provider + ")"
			}
		}
	}
	return p.current.Model
}

// ensureCatalogLocked fetches the server's models and provider commands once
// per connection; mu must be held.
func (p *Pane) ensureCatalogLocked() {
	if p.catalog != nil || p.catalogLoading || p.conn == nil {
		return
	}
	p.catalogLoading = true
	conn, id := p.conn, p.subID
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		cat, err := p.cfg.Catalog(ctx, conn)
		p.mu.Lock()
		if id == p.subID {
			p.catalogLoading = false
			if err != nil {
				p.note = "server catalog: " + err.Error()
			} else {
				p.catalog = &cat
			}
			p.gen++
		}
		p.mu.Unlock()
		p.signal()
	}()
}

// pendingQuestionLocked is the question request the agent still waits on,
// or nil; mu must be held.
func (p *Pane) pendingQuestionLocked() *t3client.TurnItem {
	for _, it := range slices.Backward(p.items) {
		if it.Type != "user_input_request" || it.RequestID == "" || it.Done() || len(it.Questions) == 0 {
			continue
		}
		if r, known := p.requests[it.RequestID]; known && !r.Pending() {
			continue
		}
		return &it
	}
	return nil
}

// answerLocked records one answer to the agent's open question and sends the
// request once every question has one; mu must be held.
func (p *Pane) answerLocked(raw string) {
	item := p.pendingQuestionLocked()
	if item == nil {
		p.note = "the agent is not asking a question"
		return
	}
	if p.answersFor != item.RequestID {
		p.answersFor, p.answers = item.RequestID, map[string]any{}
	}
	q := nextQuestion(item.Questions, p.answers)
	if q == nil {
		p.note = "every question is answered already"
		return
	}
	value, err := parseAnswer(*q, raw)
	if err != nil {
		p.note = err.Error()
		return
	}
	p.answers[q.ID] = value
	if next := nextQuestion(item.Questions, p.answers); next != nil {
		p.note = fmt.Sprintf("answered %d of %d; next: %s", len(p.answers), len(item.Questions), firstLine(next.Question))
		return
	}
	answers := p.answers
	p.answers, p.answersFor = nil, ""
	p.dispatchLocked(t3client.AnswerCommand(p.threadID, item.RequestID, answers), "answer")
}

// nextQuestion is the first question without an answer, or nil.
func nextQuestion(qs []t3client.Question, answered map[string]any) *t3client.Question {
	for i := range qs {
		if _, ok := answered[qs[i].ID]; !ok {
			return &qs[i]
		}
	}
	return nil
}

// parseAnswer reads an answer to q: an option number, several numbers
// ("1,3") for a multi-select question, or free text.
func parseAnswer(q t3client.Question, raw string) (any, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("/answer <number>, /answer 1,3 for several, or /answer <text>")
	}
	nums, numeric := parseNumbers(raw)
	if !numeric {
		return raw, nil
	}
	values := make([]string, 0, len(nums))
	for _, n := range nums {
		if n < 1 || n > len(q.Options) {
			return nil, fmt.Errorf("pick a number from 1 to %d", len(q.Options))
		}
		values = append(values, q.Options[n-1].Answer())
	}
	if q.MultiSelect {
		return values, nil
	}
	if len(values) > 1 {
		return nil, errors.New("this question takes one answer")
	}
	return values[0], nil
}

// parseNumbers reads "2" or "1, 3" as option numbers.
func parseNumbers(s string) ([]int, bool) {
	var out []int
	for _, part := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' }) {
		n, err := strconv.Atoi(part)
		if err != nil {
			return nil, false
		}
		out = append(out, n)
	}
	return out, len(out) > 0
}

// attachCmd attaches an image file by path.
func (p *Pane) attachCmd(arg string) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		p.note = "/attach <image file>; ctrl+v pastes an image from the clipboard"
		return
	}
	p.attachFileLocked(arg, true)
}

// workCmd toggles between the quiet summary of the agent's work and every step.
func (p *Pane) workCmd(string) {
	p.showWork = !p.showWork
	if p.showWork {
		p.note = "showing every step; /work hides them again"
	} else {
		p.note = "work is summarized; /work shows every step"
	}
}
