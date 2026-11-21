package chat

import (
	"slices"
	"strings"
	"unicode/utf8"
)

// sanitize drops what a terminal would act on instead of showing: escape
// sequences, 7-bit and 8-bit (CSI, OSC such as a clipboard write or a
// window title, DCS, SOS, PM, APC), and every other control character but
// the line break and the tab. A CR LF becomes a line break; a lone CR,
// which would move the cursor back over text already drawn, is dropped, and
// so are invalid UTF-8 bytes, since a raw 0x9b is a CSI to an 8-bit
// terminal. Text the backend or the user supplied passes through it before
// any row is built from it, so no row builder has to remember to.
func sanitize(s string) string {
	if clean(s) {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			i++
		case r == 0x1b:
			i = skipEscape(s, i+1)
		case r == 0x9b: // CSI
			i = skipCSI(s, i+size)
		case r == 0x90 || r == 0x98 || r == 0x9d || r == 0x9e || r == 0x9f: // DCS, SOS, OSC, PM, APC
			i = skipString(s, i+size)
		case r == '\n' || r == '\t':
			b.WriteRune(r)
			i += size
		case r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f): // CR among them: a CR LF keeps its LF
			i += size
		default:
			b.WriteString(s[i : i+size])
			i += size
		}
	}
	return b.String()
}

// clean reports text sanitize leaves as it is, without allocating: valid
// UTF-8 with no control character but the line break and the tab.
func clean(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c < 0x20 && c != '\n' && c != '\t', c == 0x7f:
			return false
		case c == 0xc2 && i+1 < len(s) && s[i+1] < 0xa0: // U+0080…U+009F, the C1 controls
			return false
		}
	}
	return utf8.ValidString(s)
}

// skipEscape is where the escape sequence whose ESC came just before i
// ends: a CSI, a control string, or ESC with its intermediates and final
// byte. A bare ESC ends at once.
func skipEscape(s string, i int) int {
	if i >= len(s) {
		return i
	}
	switch s[i] {
	case '[':
		return skipCSI(s, i+1)
	case ']', 'P', 'X', '^', '_':
		return skipString(s, i+1)
	}
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x2f {
		i++
	}
	if i < len(s) && s[i] >= 0x30 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipCSI is where the CSI whose parameters start at i ends: past its
// parameter and intermediate bytes and its final byte. A byte that cannot
// belong to one ends it there, and is kept.
func skipCSI(s string, i int) int {
	for i < len(s) && s[i] >= 0x20 && s[i] <= 0x3f {
		i++
	}
	if i < len(s) && s[i] >= 0x40 && s[i] <= 0x7e {
		i++
	}
	return i
}

// skipString is where the control string whose body starts at i ends: past
// its BEL or string terminator (ESC \ or U+009C). One never terminated runs
// to the end of the text, which a terminal would swallow as well.
func skipString(s string, i int) int {
	for ; i < len(s); i++ {
		switch {
		case s[i] == 0x07:
			return i + 1
		case s[i] == 0x1b && i+1 < len(s) && s[i+1] == '\\':
			return i + 2
		case s[i] == 0xc2 && i+1 < len(s) && s[i+1] == 0x9c:
			return i + 2
		}
	}
	return i
}

// scrub sanitizes *s in place and records whether that changed it.
func scrub(s *string, changed *bool) {
	if t := sanitize(*s); t != *s {
		*s, *changed = t, true
	}
}

// scrubAll is ss sanitized, ss itself when nothing in it changed, so a
// slice the backend still holds is never written to.
func scrubAll(ss []string) []string {
	out := ss
	for i, s := range ss {
		if t := sanitize(s); t != s {
			if &out[0] == &ss[0] {
				out = slices.Clone(ss)
			}
			out[i] = t
		}
	}
	return out
}

// sanitized is the conversation with every text the backend wrote
// sanitized. Ids and answer values are left exact, since they go back to
// the backend; the values are sanitized where they are shown. What the
// backend shows is its own, so a slice that needs a change is copied first.
func sanitized(c Conversation) Conversation {
	c.Title = sanitize(c.Title)
	c.State = AgentState(sanitize(string(c.State)))
	c.Entries = scrubEntries(c.Entries)
	if slices.ContainsFunc(c.Queued, func(q Queued) bool { return !clean(q.Text) }) {
		c.Queued = slices.Clone(c.Queued)
		for i := range c.Queued {
			c.Queued[i].Text = sanitize(c.Queued[i].Text)
		}
	}
	c.Activity = scrubActivity(c.Activity)
	c.Settings.Mode = sanitize(c.Settings.Mode)
	c.Settings.Model = sanitize(c.Settings.Model)
	c.Settings.Details = scrubAll(c.Settings.Details)
	if c.Secret != nil {
		if s, changed := scrubSecret(*c.Secret); changed {
			c.Secret = &s
		}
	}
	return c
}

// scrubEntries is the entries sanitized, copied only when one changed.
func scrubEntries(in []Entry) []Entry {
	out := in
	for i, e := range in {
		s, changed := scrubEntry(e)
		if !changed {
			continue
		}
		if &out[0] == &in[0] {
			out = slices.Clone(in)
		}
		out[i] = s
	}
	return out
}

// scrubEntry is one entry sanitized, and whether that changed it.
func scrubEntry(e Entry) (Entry, bool) {
	changed := false
	switch e := e.(type) {
	case UserMessage:
		scrub(&e.Text, &changed)
		return e, changed
	case AgentMessage:
		scrub(&e.Markdown, &changed)
		return e, changed
	case Plan:
		scrub(&e.Markdown, &changed)
		return e, changed
	case TodoList:
		if slices.ContainsFunc(e.Steps, func(s TodoStep) bool { return !clean(s.Text) }) {
			e.Steps = slices.Clone(e.Steps)
			for i := range e.Steps {
				e.Steps[i].Text = sanitize(e.Steps[i].Text)
			}
			changed = true
		}
		return e, changed
	case Approval:
		scrub(&e.Prompt, &changed)
		scrub(&e.Reason, &changed)
		return e, changed
	case Questions:
		qs := e.Questions
		for i := range qs {
			q, qChanged := scrubQuestion(qs[i])
			if !qChanged {
				continue
			}
			if !changed {
				e.Questions, changed = slices.Clone(qs), true
			}
			e.Questions[i] = q
		}
		return e, changed
	case Failure:
		scrub(&e.Text, &changed)
		return e, changed
	case Step:
		scrub(&e.Text, &changed)
		scrub(&e.Why, &changed)
		return e, changed
	case SecretRequest:
		return scrubSecret(e)
	}
	return e, false
}

// scrubQuestion is one question sanitized, its option values left exact.
func scrubQuestion(q Question) (Question, bool) {
	changed := false
	scrub(&q.Header, &changed)
	scrub(&q.Text, &changed)
	if slices.ContainsFunc(q.Options, func(o Option) bool { return !clean(o.Label) || !clean(o.Description) }) {
		q.Options = slices.Clone(q.Options)
		for i := range q.Options {
			q.Options[i].Label = sanitize(q.Options[i].Label)
			q.Options[i].Description = sanitize(q.Options[i].Description)
		}
		changed = true
	}
	return q, changed
}

// scrubSecret is a secret request sanitized, its id left exact.
func scrubSecret(s SecretRequest) (SecretRequest, bool) {
	changed := false
	scrub(&s.Label, &changed)
	scrub(&s.Reason, &changed)
	scrub(&s.URL, &changed)
	scrub(&s.Placeholder, &changed)
	scrub(&s.Status, &changed)
	return s, changed
}

// scrubActivity is the activity sanitized, copied only when an entry
// changed.
func scrubActivity(in []Activity) []Activity {
	out := in
	for i, a := range in {
		changed := false
		scrub(&a.Title, &changed)
		scrub(&a.Status, &changed)
		scrub(&a.Progress, &changed)
		if d := scrubAll(a.Details); len(d) > 0 && &d[0] != &a.Details[0] {
			a.Details, changed = d, true
		}
		if !changed {
			continue
		}
		if &out[0] == &in[0] {
			out = slices.Clone(in)
		}
		out[i] = a
	}
	return out
}
