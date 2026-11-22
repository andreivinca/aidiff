package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime/debug"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/term"
	"github.com/jessevdk/go-flags"
	"github.com/muesli/termenv"

	"github.com/umputun/revdiff/app/annotation"
	"github.com/umputun/revdiff/app/diff"
	"github.com/umputun/revdiff/app/fsutil"
	"github.com/umputun/revdiff/app/keymap"
	"github.com/umputun/revdiff/app/t3client"
	"github.com/umputun/revdiff/app/theme"
	"github.com/umputun/revdiff/app/ui"
)

var revision = "unknown"

const exitCodeAnnotations = 10

func main() {
	opts, parseErr := parseArgs(os.Args[1:])
	if parseErr != nil {
		var flagsErr *flags.Error
		if errors.As(parseErr, &flagsErr) && flagsErr.Type == flags.ErrHelp {
			os.Exit(0)
		}
		if !errors.As(parseErr, &flagsErr) {
			fmt.Fprintf(os.Stderr, "error: %v\n", parseErr)
		}
		os.Exit(1)
	}

	// early-exit commands that don't need theme resolution
	if opts.Version {
		info, _ := debug.ReadBuildInfo()
		fmt.Printf("version: %s\n", buildVersion(revision, info))
		os.Exit(0)
	}

	if opts.DumpConfig {
		dumpConfig(os.Args[1:], os.Stdout)
		os.Exit(0)
	}

	if opts.DumpKeys {
		km := keymap.LoadOrDefault(resolveFlagPath(os.Args[1:], "keys", "REVDIFF_KEYS", defaultKeysPath))
		if err := km.Dump(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	themesDir := defaultThemesDir()
	cat := theme.NewCatalog(themesDir)
	done, thErr := handleThemes(&opts, cat, os.Stdout, os.Stderr)
	if thErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", thErr)
		os.Exit(1)
	}
	if done {
		os.Exit(0)
	}

	if opts.DumpTheme {
		colors := collectColors(opts)
		th := theme.Theme{Colors: colors, ChromaStyle: opts.ChromaStyle}
		if err := th.Dump(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	if opts.T3Threads {
		if err := listT3Threads(opts, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		os.Exit(0)
	}

	code, err := run(opts)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if code != 0 {
		os.Exit(code)
	}
}

func buildVersion(rev string, info *debug.BuildInfo) string {
	if rev != "" && rev != "unknown" {
		return rev
	}
	if info != nil && info.Main.Version != "" && info.Main.Version != "(devel)" {
		return info.Main.Version
	}
	return "unknown"
}

func run(opts options) (int, error) {
	// force lipgloss to truecolor when colors are enabled. revdiff's raw-ANSI
	// helpers (style.ansiColor) always emit truecolor, but lipgloss respects
	// the termenv-detected profile, which can downgrade to ANSI256 / ANSI in
	// tmux or terminals where TERM/COLORTERM detection regresses. The mismatch
	// makes lipgloss-rendered colors (pane borders, file tree fg) look wrong
	// while raw-ANSI paths (line prefix wrap, overlay title injection) render
	// correctly. Forcing truecolor unifies the two paths.
	if !opts.NoColors {
		lipgloss.SetColorProfile(termenv.TrueColor)
	}

	// registered first so it runs last, after every other deferred close
	deadline := newExitDeadline()
	defer deadline.disarm()

	plan, err := planStart(opts, tabsStore()) // the folder argument, or the saved tab a run that names nothing starts on
	if err != nil {
		return 0, err
	}
	opts = plan.opts

	store := annotation.NewStore()
	deps := newReviewDeps(opts)

	programOptions := []tea.ProgramOption{tea.WithAltScreen(), tea.WithoutSignalHandler()}
	tuiOut, err := (tuiOutput{
		stdout:     os.Stdout,
		isTerminal: term.IsTerminal,
		openTTY: func() (*os.File, error) {
			// stdin mode and Bubble Tea's default input fallback open a separate
			// read handle; each side owns and closes its handle independently.
			return os.OpenFile("/dev/tty", os.O_WRONLY, 0)
		},
	}).open()
	if err != nil {
		return 0, err
	}
	if tuiOut != os.Stdout {
		defer func() { _ = tuiOut.Close() }()
	}
	programOptions = append(programOptions, tea.WithOutput(tuiOut))
	if !opts.NoMouse {
		programOptions = append(programOptions, tea.WithMouseCellMotion())
	}
	description, err := resolveDescription(opts)
	if err != nil {
		return 0, err
	}

	src, err := plan.source()
	if err != nil {
		return 0, err
	}
	defer src.close()
	reviewDescription := src.reviewDescription(description)
	local, diffs := newLocalDiff(deps, src, reviewDescription), &localDiffs{}
	t3p := newT3Palette(deps, description, src, store, newLocalConfig(deps, src, diffs, local), plan) // owns src's T3 session from here on
	defer t3p.Close()
	deps.views.set(switchView(t3p, local, diffs))
	deps.views.setChoices(diffChoices(t3p, local, diffs))
	deps.views.setSend(t3p.SendAnnotations)
	if src.tty != nil {
		programOptions = append(programOptions, tea.WithInput(src.tty))
	}

	if opts.Annotations != "" {
		if perr := preloadAnnotations(opts.Annotations, store, src.renderer, opts.ref(), opts.Staged,
			src.untrackedFn, src.untrackedRenamesFn, src.workDir, os.Stderr); perr != nil {
			return 0, perr
		}
	}
	preloaded := store.Count() > 0

	var model tea.Model // none for a saved T3 tab: the split opens it (t3p.Opening)
	if plan.tab == nil {
		if model, err = deps.newModel(src, store, reviewDescription); err != nil {
			return 0, err
		}
	}

	p := tea.NewProgram(newSplit(opts, deps.res, model, t3p, local, diffs, tuiOut), append(programOptions, splitProgramOptions()...)...)
	t3p.Start(p, src.watch(opts, p))
	guard := &shutdownGuard{deadline: deadline} // a signal bounds the exit even when the quit never lands
	stop := guard.watch(forceQuitter{p})
	defer stop()
	final, runErr := runSplit(p, tuiOut, deadline)
	// capture the signal flag once: a signal can land between reads, so a graceful
	// runErr==nil exit must not observe wasSignaled flipping to true mid-tail.
	signaled := guard.wasSignaled()
	// restore default signal disposition before finalize. saveHistory shells out
	// to git and writes files, so a slow or hung finalize must stay interruptible
	// by a second signal (default disposition terminates) rather than being caught
	// and swallowed by the guard. defer stop() above stays as a panic safety net —
	// stop is idempotent.
	stop()
	if runErr == nil || signaled {
		saveTabs(t3p, final) // as the history's safety net runs: the window closing is an exit too
	}
	finalModel := closeSplit(final)

	// persist annotations: history safety net + optional -o handoff. a
	// signal-driven exit can still surface a TUI error — a PTY hangup returns
	// EIO that races the guard's QuitMsg — so the history safety net must run
	// whenever the model is available and the exit was graceful or signaled.
	if m, ok := finalModel.(ui.Model); ok && (runErr == nil || signaled) {
		hist := src.exitHistory(opts, t3p, diffs) // the review on screen may not be the one the run opened
		return deadline.result(finalize(finalizeReq{
			opts:        hist.opts,
			annotations: m.Store().FormatOutput(),
			files:       m.Store().Files(),
			discarded:   m.Discarded(),
			preloaded:   preloaded,
			flushed:     m.OutputFlushed(),
			gitRoot:     hist.gitRoot,
			workDir:     hist.workDir,
			t3Path:      hist.t3Path,
			signaled:    signaled,
			loadErr:     m.LoadError(),
			stdout:      os.Stdout,
		}))
	}
	if runErr != nil {
		return 0, fmt.Errorf("TUI error: %w", runErr)
	}
	return 0, nil
}

type tuiOutput struct {
	stdout     *os.File
	isTerminal func(uintptr) bool
	openTTY    func() (*os.File, error)
}

// open keeps Bubble Tea's display traffic out of redirected stdout, which is
// reserved for the final annotation stream. Terminal stdout is returned as-is.
func (r tuiOutput) open() (*os.File, error) {
	if r.isTerminal(r.stdout.Fd()) {
		return r.stdout, nil
	}
	tty, err := r.openTTY()
	if err != nil {
		return nil, fmt.Errorf("revdiff requires an interactive terminal for the TUI: %w", err)
	}
	return tty, nil
}

type finalizeReq struct {
	opts        options
	annotations string
	files       []string
	discarded   bool
	preloaded   bool
	flushed     bool
	gitRoot     string
	workDir     string
	t3Path      string // a T3 review: its server and folder, filed in place of a local path
	signaled    bool
	loadErr     error
	stdout      io.Writer
}

// finalize persists the review after p.Run() joins. A discarded review writes
// nothing, and so does one with no annotations unless --annotations preloaded
// some or an O flush wrote the -o file: a graceful exit that deleted all of them
// writes the empty output, so a stale -o file cannot bring them back on the next
// load or hand them to the caller. Otherwise the history
// safety-net save always runs; a signal-driven exit (r.signaled) stops there —
// history only, never the -o handoff — while a graceful exit also writes the
// annotation output.
// A failed file-list load with no annotations returns the load error, so callers
// never read an exit 0 with empty output as a clean review; it still clears a
// stale output file first, since a failed R reload is one way to end up there.
func finalize(r finalizeReq) (int, error) {
	if r.loadErr != nil && r.annotations == "" {
		if r.clearsStaleOutput() {
			if _, err := writeAnnotationOutput(annotationOutputReq{opts: r.opts, output: "", stdout: r.stdout}); err != nil {
				return 0, err
			}
		}
		return 0, fmt.Errorf("load changed files: %w", r.loadErr)
	}
	if r.discarded {
		return 0, nil
	}
	if r.annotations == "" {
		if !r.clearsStaleOutput() {
			return 0, nil
		}
		return writeAnnotationOutput(annotationOutputReq{opts: r.opts, output: "", stdout: r.stdout})
	}
	saveHistory(histReq{opts: r.opts, annotations: r.annotations, gitRoot: r.gitRoot, workDir: r.workDir, files: r.files, t3Path: r.t3Path})
	if r.signaled {
		return 0, nil
	}
	return writeAnnotationOutput(annotationOutputReq{opts: r.opts, output: r.annotations, stdout: r.stdout})
}

// clearsStaleOutput reports whether an exit with no annotations must write the
// empty output: earlier notes reached it through --annotations or an O flush, and
// the exit was graceful.
func (r finalizeReq) clearsStaleOutput() bool {
	return (r.preloaded || r.flushed) && !r.discarded && !r.signaled
}

type annotationOutputReq struct {
	opts   options
	output string
	stdout io.Writer
}

func writeAnnotationOutput(r annotationOutputReq) (int, error) {
	code := annotationExitCode(r.opts.ExitCodeOnAnnotations, r.output)
	if r.opts.Output != "" {
		if err := fsutil.AtomicWriteFile(r.opts.Output, []byte(r.output)); err != nil {
			return 0, fmt.Errorf("write output: %w", err)
		}
		return code, nil
	}
	if _, err := fmt.Fprint(r.stdout, r.output); err != nil {
		return 0, fmt.Errorf("write output: %w", err)
	}
	return code, nil
}

func annotationExitCode(enabled bool, output string) int {
	if enabled && output != "" {
		return exitCodeAnnotations
	}
	return 0
}

// reloadApplicable returns false when --stdin is active: the stream has already
// been consumed and cannot be re-read. All other modes support reload.
func reloadApplicable(opts options) bool {
	return !opts.Stdin
}

func sourceEditorPolicy(opts options, workDir string) ui.SourceEditorPolicy {
	switch {
	case opts.Stdin:
		return ui.SourceEditorPolicy{} // unsupported
	case opts.compareAbsNew != "":
		// Always prefer --compare-new in compare mode.
		return ui.SourceEditorPolicy{
			Available: true,
			Root:      filepath.Dir(opts.compareAbsNew),
			ExactPath: opts.compareAbsNew,
		}
	case workDir != "":
		// a single ref is diffed against the working tree, so the edited file is the reviewed new side
		worktreeReview := !opts.Staged && !opts.twoRef()
		return ui.SourceEditorPolicy{
			Available: true,
			Root:      workDir,
			// When reviewing worktree changes, reload after edits and disallow
			// editing annotated files because edits can orphan comments.
			ReloadAfterCleanExit:         worktreeReview,
			DisallowAnnotatedFileEditing: worktreeReview,
		}
	default:
		return ui.SourceEditorPolicy{}
	}
}

// resolveKeysPath returns the effective keybindings file path, falling back
// to defaultKeysPath() when --keys was not set. Extracted from run() to keep
// its cyclomatic complexity under the gocyclo limit after compare-mode
// dispatch was added.
func resolveKeysPath(opts options) string {
	if opts.Keys == "" {
		return defaultKeysPath()
	}
	return opts.Keys
}

// commitsApplicable returns true when the unified info popup can include a
// commit-log section: a VCS-backed log source must be present and the mode
// must be ref-based (no stdin, staged, all-files, or empty ref). Computed
// once in the composition root so the Model does not re-derive from CLI
// flags. --only is fine when combined with a ref in a real repo; the empty
// ref check excludes the standalone --only / FileReader case where the
// commitLogger is nil anyway.
func commitsApplicable(opts options, cl diff.CommitLogger) bool {
	if cl == nil {
		return false
	}
	if opts.Stdin || opts.Staged || opts.AllFiles {
		return false
	}
	return opts.ref() != ""
}

// compactApplicable returns true when the current invocation can shrink the
// VCS diff via the compact toggle. false for stdin (no VCS), all-files (no
// hunks to contextualize), and standalone file review via FileReader (pure
// context-only source with no underlying VCS). All other renderer shapes —
// *Git / *Hg / *Jj, with or without Fallback / Include / Exclude wrappers —
// qualify because the wrapper chain delegates FileDiff straight through to
// a VCS that honors contextLines.
func compactApplicable(opts options, r ui.Renderer) bool {
	if opts.Stdin || opts.AllFiles {
		return false
	}
	switch r.(type) {
	case *diff.FileReader, *diff.FolderReader, *t3client.FolderReader: // every file, whole: there are no changes to frame
		return false
	}
	return true
}
