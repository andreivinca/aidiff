package t3palette

import (
	tea "github.com/charmbracelet/bubbletea"

	"github.com/umputun/revdiff/app/splitview"
)

// Saved connections. Every T3 server the user connects to is saved, as the
// local folders are, so the connections panel lists it in later runs too:
// greyed while not connected, Enter connects to it. x forgets one, after a
// confirmation.

// SavedConfig is how the palette keeps the servers connected to.
type SavedConfig struct {
	// List lists the servers saved before, most recently connected first.
	// It is read once, when the palette is built.
	List func() []SavedServer
	// Remember saves spec, on machine, as connected now. It runs in the
	// background.
	Remember func(spec, machine string)
	// Forget removes spec from the saved servers.
	Forget func(spec string) error
}

// SavedServer is a server connected to before: spec dials it again (a
// session's Label), machine names the host it runs on.
type SavedServer struct {
	Spec    string
	Machine string
}

// initSavedLocked loads the saved servers. mu must be held or the palette
// not shared yet.
func (t *Palette) initSavedLocked() {
	if t.cfg.Saved.List == nil {
		return
	}
	for _, s := range t.cfg.Saved.List() {
		if s.Spec != "" && savedIndex(t.saved, s.Spec) < 0 {
			t.saved = append(t.saved, s)
		}
	}
}

// savedIndex is the index of spec in list, -1 when it is not there.
func savedIndex(list []SavedServer, spec string) int {
	for i, s := range list {
		if s.Spec == spec {
			return i
		}
	}
	return -1
}

// saveLocked records s's server as connected now: listed (at the end, so
// the rows stay put while the run lasts) and saved for later runs. mu must
// be held.
func (t *Palette) saveLocked(s Session) {
	spec, machine := s.Label(), s.MachineName()
	if spec == "" {
		return
	}
	if i := savedIndex(t.saved, spec); i >= 0 {
		t.saved[i].Machine = machine
	} else {
		t.saved = append(t.saved, SavedServer{Spec: spec, Machine: machine})
	}
	if remember := t.cfg.Saved.Remember; remember != nil {
		go remember(spec, machine)
	}
}

// savedRows are the connections panel's rows of the saved servers not
// connected now; labels holds the connected ones. mu must be held.
func (t *Palette) savedRowsLocked(connected map[string]bool) []connRow {
	var rows []connRow
	for _, s := range t.saved {
		if connected[s.Spec] {
			continue
		}
		name := s.Spec
		if name == localLabel {
			name = localT3Row
		}
		rows = append(rows, connRow{label: name, detail: s.Machine, spec: s.Spec})
	}
	return rows
}

// forgetPrompt asks to forget the server of row r, as x does.
func (t *Palette) forgetPrompt(r connRow) tea.Cmd {
	spec := r.spec
	detail := "Removes it from the saved connections."
	if r.session != nil {
		spec = r.session.Label()
		detail += " It stays connected until aidiff exits."
	}
	if spec == "" {
		return nil
	}
	return func() tea.Msg {
		return splitview.ConfirmMsg{Title: "Forget " + r.label + "?", Detail: detail, Confirm: "Forget", Yes: t.forget(spec, r.label)}
	}
}

// forget removes spec from the saved servers.
func (t *Palette) forget(spec, label string) tea.Cmd {
	return func() tea.Msg {
		t.mu.Lock()
		if i := savedIndex(t.saved, spec); i >= 0 {
			t.saved = append(t.saved[:i:i], t.saved[i+1:]...)
		}
		forget := t.cfg.Saved.Forget
		t.mu.Unlock()
		if forget != nil {
			if err := forget(spec); err != nil {
				return t3Error("forget "+label, err)
			}
		}
		return splitview.FlashMsg{Text: "forgot " + label}
	}
}
