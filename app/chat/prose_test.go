package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A **bold** run that a wrap cuts used to leave its markers printed on both
// rows, worst in bullets, whose hanging indent narrows every row: the rows
// were wrapped first and styled after.
func TestProse_BoldCutByAWrapKeepsItsStyle(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
	st, _ := newStyles(true)

	b := markdownRows("- A bash shell that the T3 server opened on **Nov 15 at 11:03** and kept", 40, st)
	require.Greater(t, len(b.rows), 1, "the bullet wraps")
	var shown []string
	for _, r := range b.rows {
		assert.NotContains(t, r, "**", "no marker is printed")
		shown = append(shown, strings.TrimSpace(ansi.Strip(r)))
	}
	assert.Contains(t, strings.Join(shown, " "), "Nov 15 at 11:03", "the bold text shows whole")
	joined := strings.Join(b.rows, "")
	assert.Contains(t, joined, st.bold.Render("Nov 15 at 11:03"), "the run is drawn bold, whole")
	assert.True(t, strings.HasPrefix(ansi.Strip(b.rows[1]), inset+"  "), "rows after the first hang under the text")
}

// A copy reads the shown text: a row's end counts the cells it shows, never
// the markers it no longer prints.
func TestProse_CopyMetaMeasuresShownText(t *testing.T) {
	st, _ := newStyles(false)
	b := markdownRows("see **this** now", 80, st)
	require.Len(t, b.rows, 1)
	assert.Equal(t, inset+"see this now", ansi.Strip(b.rows[0]))
	assert.Equal(t, len(inset)+len("see this now"), b.meta[0].end)
}
