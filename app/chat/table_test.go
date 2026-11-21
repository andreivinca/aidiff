package chat

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sampleTable is a table an agent wrote, as the owner saw it printed raw.
const sampleTable = `| Request the size of one "Add update" model call | Result |
|---|---|
| Loading the model | 8 s |
| Reading the prompt (3,343 tokens) | **560 s**, about 6 tokens/s |
| Writing the answer (150 tokens) | about 1.2 tokens/s |
| **Total** | **11 min 35 s** |`

// plainRows renders markdown without colors, as the rows' text.
func plainRows(t *testing.T, md string, w int) []string {
	t.Helper()
	st, _ := newStyles(false)
	b := markdownRows(md, w, st)
	require.Len(t, b.meta, len(b.rows))
	out := make([]string, len(b.rows))
	for i, r := range b.rows {
		out[i] = ansi.Strip(r)
		assert.LessOrEqual(t, ansi.StringWidth(r), w, "row %d fits the pane: %q", i, out[i])
	}
	return out
}

func TestTable_NaturalWidth(t *testing.T) {
	rows := plainRows(t, "Timing:\n\n"+sampleTable+"\n\nThat is slow.", 80)
	assert.Equal(t, []string{
		"   Timing:",
		"",
		"   ╭─────────────────────────────────────────────────┬─────────────────────────╮",
		`   │ Request the size of one "Add update" model call │ Result                  │`,
		"   ├─────────────────────────────────────────────────┼─────────────────────────┤",
		"   │ Loading the model                               │ 8 s                     │",
		"   │ Reading the prompt (3,343 tokens)               │ 560 s, about 6 tokens/s │",
		"   │ Writing the answer (150 tokens)                 │ about 1.2 tokens/s      │",
		"   │ Total                                           │ 11 min 35 s             │",
		"   ╰─────────────────────────────────────────────────┴─────────────────────────╯",
		"",
		"   That is slow.",
	}, rows)
}

func TestTable_WiderThanThePaneWraps(t *testing.T) {
	rows := plainRows(t, sampleTable, 36)
	assert.Equal(t, "   ╭───────────────┬───────────────╮", rows[0])
	assert.Equal(t, "   │ Request the   │ Result        │", rows[1])
	assert.Equal(t, "   │ size of one   │               │", rows[2], "a row grows taller rather than losing text")
	text := strings.Join(rows, " ")
	assert.NotContains(t, text, "…")
	for _, word := range strings.Fields(strings.NewReplacer("|", " ", "---", " ", "**", "").Replace(sampleTable)) {
		assert.Contains(t, text, word, "every word of the table is drawn")
	}
	for _, r := range rows {
		assert.Equal(t, 36, ansi.StringWidth(r), "the grid is as wide as it was fitted: %q", r)
	}
}

func TestFitColumns(t *testing.T) {
	w, ok := fitColumns([]int{10, 20}, 40)
	assert.True(t, ok)
	assert.Equal(t, []int{10, 20}, w, "natural widths that fit stay")
	w, ok = fitColumns([]int{5, 60, 40}, 50)
	assert.True(t, ok)
	assert.Equal(t, []int{5, 23, 22}, w, "a column under its share keeps its width, the others split the rest")
	_, ok = fitColumns([]int{30, 30, 30, 30}, 20)
	assert.False(t, ok, "too narrow for minColumn each")
}

func TestTable_Alignment(t *testing.T) {
	rows := plainRows(t, "| left | center | right |\n|:-----|:------:|------:|\n| a | b | c |", 60)
	assert.Equal(t, "   │ a    │   b    │     c │", rows[3])
	assert.Equal(t, alignments([]string{":--", ":-:", "--:", "---"}, 4), []align{alignLeft, alignCenter, alignRight, alignLeft})
}

func TestTable_InlineStylesAndWidths(t *testing.T) {
	segs := inlineSegments("**Total** in `go test` and a [link](http://x)")
	assert.Equal(t, []segment{{text: "Total", style: segBold}, {text: " in "}, {text: "go test", style: segCode},
		{text: " and a link"}}, segs)
	rows := plainRows(t, "| **Total** | 世界 | 👩‍💻 |\n|---|---|---|\n| x | y | z |", 60)
	assert.Equal(t, "   ╭───────┬──────┬────╮", rows[0], "widths are measured on what shows: 5, 4 and 2 cells")
	assert.Equal(t, "   │ Total │ 世界 │ 👩‍💻 │", rows[1])

	st, _ := newStyles(true)
	b := markdownRows("| **Total** | `code` |\n|---|---|\n| [a](u) | b |", 60, st)
	assert.Contains(t, b.rows[1], st.bold.Render("Total"))
	assert.Contains(t, b.rows[1], st.code.Render("code"))
	assert.Contains(t, ansi.Strip(b.rows[3]), "│ a ", "a link shows its text")
}

func TestTable_PipesInsideCells(t *testing.T) {
	assert.Equal(t, []string{"a | b", "`x | y`", "c"}, splitRow(`| a \| b | `+"`x | y`"+` | c |`))
	assert.Equal(t, []string{"a", "b"}, splitRow("a | b"), "outer pipes are optional")
	assert.Equal(t, []string{"`open", "b"}, splitRow("| `open | b |"), "an unclosed backtick does not hide a separator")
	rows := plainRows(t, "| expr | note |\n|---|---|\n| `a | b` | x \\| y |", 60)
	assert.Equal(t, "   │ a | b │ x | y │", rows[3])
}

func TestTable_RaggedRows(t *testing.T) {
	rows := plainRows(t, "| a | b | c |\n|---|---|---|\n| 1 |\n| 1 | 2 | 3 | 4 | 5 |", 60)
	assert.Equal(t, "   │ 1 │   │   │", rows[3], "a short row is padded")
	assert.Equal(t, "   │ 1 │ 2 │ 3 │", rows[4], "a long one loses its extra cells")
}

func TestTable_StackedInANarrowPane(t *testing.T) {
	md := "| Name | Role | Location | Notes |\n|---|---|---|---|\n| Ada Lovelace | mathematician | London | wrote the first program |\n| Alan Turing | logician | Manchester | broke codes |"
	rows := plainRows(t, md, 28)
	text := strings.Join(rows, "\n")
	assert.NotContains(t, text, "│", "no grid in a pane this narrow")
	assert.Contains(t, text, "   Name: Ada Lovelace")
	assert.Contains(t, text, "   Role: mathematician")
	assert.Contains(t, text, "   Notes: wrote the first\n     program", "a long value wraps under itself")
	assert.Contains(t, text, "\n\n   Name: Alan Turing", "records apart")
}

func TestTable_NotATable(t *testing.T) {
	assert.Equal(t, []string{"   a | b", "   c | d"}, plainRows(t, "a | b\nc | d", 40), "rows without a delimiter row are prose")
	assert.Equal(t, []string{"   heading", "   ---"}, plainRows(t, "heading\n---", 40))
	assert.Equal(t, []string{"   | a | b |"}, plainRows(t, "| a | b |", 40), "a finished reply's lone row is prose")
	assert.Equal(t, []string{"   | a | b |", "   |---|"}, plainRows(t, "| a | b |\n|---|", 40), "a delimiter row must match the header")
	rows := plainRows(t, "```\n| a | b |\n|---|---|\n```", 40)
	assert.Equal(t, "     | a | b |", rows[0], "not inside a fenced block")
}

func TestTable_Streaming(t *testing.T) {
	src := "Here:\n\n" + sampleTable + "\n\nDone."
	for _, w := range []int{80, 36, 20} {
		for i := range len(src) + 1 {
			plainRows(t, src[:i]+streamCursor, w) // every prefix renders, within the pane
		}
	}
	assert.Contains(t, plainRows(t, "| Request | Res"+streamCursor, 40)[0], "╭", "a header being written is a table already")
	partial := plainRows(t, "| Request | Result |\n|---|-"+streamCursor, 40)
	assert.Contains(t, partial[0], "╭", "so is a header over a delimiter row half written")
	assert.NotContains(t, strings.Join(partial, "\n"), "---|", "the half-written row is not shown raw")
	rows := plainRows(t, "| a | b |\n|---|---|\n| 1 | 2 |\n| 3 | fo"+streamCursor, 40)
	assert.Equal(t, "   │ 3 │ fo▍ │", rows[4], "a row being written shows what it has, the cursor after it")
}

func TestPane_CopiesATableAsItsCells(t *testing.T) {
	conv := Conversation{State: AgentIdle, Entries: []Entry{AgentMessage{Markdown: "Timing:\n\n" + sampleTable + "\n\nslow"}}}
	p, f := newTestPane(t, 36, 50, conv)
	rows := strings.Split(view(p), "\n")
	first, last := rowWith(rows, "Timing:"), rowWith(rows, "slow")
	require.Greater(t, last, first)
	assert.Equal(t, "Timing:\n\n"+
		"Request the size of one \"Add update\" model call\tResult\n"+
		"Loading the model\t8 s\n"+
		"Reading the prompt (3,343 tokens)\t560 s, about 6 tokens/s\n"+
		"Writing the answer (150 tokens)\tabout 1.2 tokens/s\n"+
		"Total\t11 min 35 s\n\nslow", copyRows(t, p, f, first, last),
		"each record once, its cells tab separated, without borders, however many rows it took")
	mid := rowWith(rows, "prompt (3,343")
	assert.Equal(t, "Reading the prompt (3,343 tokens)\t560 s, about 6 tokens/s", copyRows(t, p, f, mid, mid),
		"a row of a wrapped record copies the whole record")
}

func TestPane_TableAndCodeTextIsSanitized(t *testing.T) {
	conv := Conversation{State: AgentIdle, Entries: []Entry{AgentMessage{
		Markdown: "| a | b |\n|---|---|\n| x" + hostile + " | y |\n\n```\ncode" + hostile + "\n```"}}}
	p, _ := newTestPane(t, 60, 30, conv)
	assertNoEscapes(t, p.Render(true))
	assert.Contains(t, view(p), "│ x │ y │")
}

func TestPane_TableIsDrawnAgainOnResize(t *testing.T) {
	conv := Conversation{State: AgentIdle, Entries: []Entry{AgentMessage{Markdown: "Timing:\n\n" + sampleTable}}}
	p, _ := newTestPane(t, 90, 40, conv)
	border := func() string {
		rows := strings.Split(view(p), "\n")
		return strings.TrimRight(rows[rowWith(rows, "╭")], " ")
	}
	assert.Equal(t, 80, ansi.StringWidth(border()), "natural widths in a wide pane")
	for _, w := range []int{54, 40} {
		p.Resize(w, 40)
		for _, r := range strings.Split(view(p), "\n") {
			assert.LessOrEqual(t, ansi.StringWidth(r), w)
		}
		b := border()
		assert.True(t, strings.HasSuffix(b, "╮"), "the grid is drawn again for the new width, whole: %q", b)
		assert.LessOrEqual(t, ansi.StringWidth(b), w)
		assert.NotContains(t, view(p), "\n     ─", "no border is wrapped onto a second row")
	}
	p.Resize(22, 40)
	assert.NotContains(t, transcriptView(p), "╭", "too narrow for a grid: the records are stacked")
	assert.Contains(t, transcriptView(p), "Result: 8 s")
	p.Resize(90, 40)
	assert.Equal(t, 80, ansi.StringWidth(border()), "and back")
}
