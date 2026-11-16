# Architecture

aidiff is a fork of [revdiff](https://github.com/umputun/revdiff). We use revdiff as the **review engine**: diffs, the file tree, annotations, themes and keys. Our work is what goes **around** it: a split view with terminal and chat panes, a command palette, live refresh, folder browsing, and the T3 Code client. This document describes our part and how it plugs into revdiff. For revdiff's internals, see the upstream project.

## 1. System overview

```
                     ┌────────────────────────── aidiff process ───────────────────────────┐
 CLI / env / INI ───▶│ app/revdiff  (composition root)                                      │
                     │   parse options → pick source → build review → wrap in splitview     │
                     │                                                                      │
                     │  ┌──────────── splitview.Model (root tea.Model) ──────────────────┐  │
                     │  │  palette · quit guard · focus · drag-resize · refresh gating    │  │
                     │  │  ┌────────────────┐  ┌──────────────┐  ┌──────────────┐          │  │
                     │  │  │ revdiff review │  │ termpane     │  │ t3chat       │  …       │  │
                     │  │  │ (ui.Model)     │  │ PTY + vt     │  │ T3 transcript│          │  │
                     │  │  └───────┬────────┘  └──────┬───────┘  └──────┬───────┘          │  │
                     │  └──────────┼──────────────────┼─────────────────┼──────────────────┘  │
                     │   revdiff renderer        $SHELL / ssh       t3client.Conn             │
                     │   or t3client.Renderer                                                 │
                     │   app/watch ──FilesChangedMsg──▶ splitview ──RefreshMsg──▶ review     │
                     └────────────────────────────────────────────────────────────────────────┘
                                                          ▼                    ▼
                                                    local / remote shell   T3 Code server
```

## 2. What is ours and what is revdiff's

| Layer | Packages | Owner |
|---|---|---|
| Review engine: diff sources, review UI, annotations, themes, keymap, history, editor | `app/diff`, `app/ui/**`, `app/annotation`, `app/keymap`, `app/theme`, `app/highlight`, `app/history`, `app/editor`, `app/handoff`, `app/review`, `app/fsutil` | **revdiff** (we merge upstream) |
| Split view, palette, quit guard, refresh gating | `app/splitview` | **aidiff** |
| Embedded terminal | `app/termpane` | **aidiff** |
| Live refresh | `app/watch` | **aidiff** |
| T3 Code client | `app/t3client` | **aidiff** |
| T3 chat pane | `app/t3chat` | **aidiff** |
| Clipboard | `app/clipboard` | **aidiff** |
| Wiring | `app/revdiff/t3*.go`, `watch.go`, parts of `main.go`, `config.go`, `renderer_setup.go` | **aidiff** additions to the upstream root |

Small hooks we added inside revdiff code:
- `app/ui/refresh.go`: `RefreshMsg` and `CanRefresh()`
- `app/ui/folder.go`, and folder rows in `app/ui/sidepane`: folder browsing
- `MachineIdentityMsg` / `T3Mode` in `app/ui`: status-bar identity
- `app/diff/folder.go`: `FolderReader`, the lazy folder source
- `app/diff/watchdirs.go`: which directories to watch in a git repo

**Rule:** keep fork code in our own packages and keep the hooks into revdiff as small as possible, so upstream merges stay cheap.

## 3. How we use revdiff

From our side, revdiff is three things:

1. **A source of diffs.** Something that implements `ChangedFiles(ref, staged)` and `FileDiff(req)`. The composition root picks one for the mode: a git, hg or jj renderer, a file list, stdin, two-file compare, our `FolderReader`, or our `t3client.Renderer`.
2. **A review model** (`ui.Model`), built from `ui.ModelConfig`. We fill in the config and never change how the model works inside.
3. **Output.** When the program ends we read the annotation store from the model and let revdiff's `finalize` write history and `-o`/stdout.

Because `t3client.Renderer` implements the same interface, a T3 thread opens in the review exactly like a local repository.

## 4. Startup sequence

`app/revdiff/main.go`, `run()`:
1. Parse options: the revdiff flags plus ours (terminal, chat, watch, T3).
2. Pick the source in this order: **T3**, then compare, then stdin, then VCS detection. With no repository it uses **FolderReader** (no `--only`) or the file reader (`--only`). In T3 mode the connection is made here, before the full-screen UI starts, so connection errors print as plain text.
3. Build the revdiff review model.
4. Build the T3 palette state (`newT3Palette`) and wrap the review: `splitview.New(review, {WidthPercent, OpenOnStart, Start, RefreshMsg, Commands})`. The review is always wrapped, so ctrl+p works in every mode.
5. Start Bubble Tea at 120 FPS, for terminal-pane responsiveness.
6. Start the watcher for the source (`src.watch`). In T3 mode with a thread selected, open the chat pane.
7. On exit, unwrap splitview, close every pane, and hand the review back to revdiff's finalization. `t3p.history()` decides which review's annotations are saved, because the user may have switched T3 threads.

## 5. Split view (`app/splitview`)

`splitview.Model` is the root model. It holds the review (`inner`) and a list of right-hand panes. Each pane implements:

```go
type Terminal interface {
    Updates() <-chan struct{}; Exited() bool
    SendText(string); SendKey(uv.KeyPressEvent); Paste(string); SendMouse(uv.MouseEvent)
    Resize(w, h int); Render() string; Close()
}
```

`termpane.Pane` and `t3chat.Pane` implement it. A pane can also implement `QuitNoted` (a note for the quit dialog) and `MouseCaptured` (the pane keeps the mouse while dragging).

How it works:
- **The review sees only its own column.** It is told the window is the left column's width, so its layout and click handling are unchanged.
- **Global keys come first:** ctrl+] cycles focus, ctrl+p opens the palette, ctrl+q opens the quit guard. All other keys go to the focused pane. For a terminal pane they are re-encoded as terminal bytes (`keys.go`).
- **Layout:** panes are full-height columns. Together they take `--terminal-width` percent (default 45). A new pane gets an equal share and the others shrink proportionally. Minimum widths are 20 columns for the review and 10 per pane. Every separator can be dragged. A pane whose program exits is removed.
- **Quit guard:** a `tea.Quit` from the review, including one inside a batch, becomes a confirmation dialog whenever panes are open.
- **Refresh gating:** a `FilesChangedMsg` marks a refresh as pending. It is delivered as `RefreshMsg` only when the review's `CanRefresh()` is true, which is not the case while the user is typing, a popup is open, or a reload is running.
- **Replacing the review:** `ReplaceInnerMsg{Model, Retire}` swaps in a different review. This is how T3 thread switching works, and `Retire` saves the old review's history.
- **Palette:** `Config.Commands()` is evaluated every time the palette renders, so its entries reflect live state. A command can open a picker, a prompt, or a flash message.
- **Caching:** the review's view is cached until the review gets a message, so typing in a terminal doesn't redraw the diff.

## 6. Terminal pane (`app/termpane`)

```
 keys ─▶ vt encoder ─▶ emulator input ──▶ pty ──▶ child (shell / ssh)
 child ─▶ pty ──▶ emulator (under mutex) ──▶ notify (≤ 1 per 16 ms) ──▶ Updates()
```

- Uses `creack/pty` and `charmbracelet/x/vt`, with `TERM=xterm-256color` and 5000 lines of scrollback.
- Redraws are capped at one per 16 ms, but the first change after a quiet spell is shown at once. `Render` is cached until the screen changes.
- `Close` sends SIGHUP to every process in the child's session, waits 1 s, then sends SIGKILL. On macOS, where there is no `/proc`, it signals the process group instead.
- The start command and directory come from `app/revdiff/t3_terminal.go`:
  - When the live T3 session goes through SSH, it runs `ssh -t host` and starts a login shell in the thread's directory.
  - Otherwise it runs `--terminal-command` in the reviewed directory.

## 7. Live refresh (`app/watch`)

| Source | Watch |
|---|---|
| git repository | fsnotify on the work tree, skipping ignored directories. Also watches the git dir: HEAD, the index, `refs/heads`. |
| Folder (no repository) | Shallow: the root, plus the folders the user has expanded. The list is updated through `FolderReader.SetDirectoryWatcher`. |
| T3 thread | No filesystem watch. `t3client.WatchThread` fires when a run starts or finishes. |
| hg, jj, stdin, compare, `--only` without a repository | None |

There is no timer debounce. A burst of changes collapses into one pending notification, and splitview's gating plus revdiff's quiet reload handle the rest. The reload keeps annotations and the cursor position. If the watcher can't start, aidiff logs a warning and the review works without live refresh. `--no-watch` turns it off.

## 8. T3 client and chat

[T3_INTEGRATION.md](T3_INTEGRATION.md) has the full detail.
- **`t3client.Conn`:** a WebSocket with a read goroutine and a ping goroutine. Stream callbacks run on the read goroutine and must not block, so callers hand work off with `go p.Send(...)`.
- **`t3client.Renderer`:** adapts a thread's diff preview to revdiff's renderer interface.
- **`t3chat.Pane`:** one mutex guards its state. Each subscription has an ID, and events from a replaced subscription are dropped. Network calls run in goroutines that re-check the subscription ID before applying their result.
- **Palette commands:** connect, threads, projects, open chat, send message, send annotations, reconnect. They are built in `app/revdiff/t3_palette.go`.

## 9. Design decisions

| Decision | Rationale |
|---|---|
| Wrap the revdiff review instead of modifying it | Upstream merges stay cheap, and the review knows nothing about panes. |
| Fork features live in their own packages | Keeps the same merge benefit, and keeps OS work (PTY, sockets, fsnotify, clipboard) out of the review UI. |
| T3 thread exposed as a revdiff renderer | T3 reuses the whole review UI with no special cases. |
| Quiet refresh separate from revdiff's `R` reload | `R` drops annotations on purpose. An automatic refresh must never lose work. |
| Refresh waits on `CanRefresh()` | The view must not reload while the user is typing or has a popup open. |
| No debounce timer in the watcher | Gating plus coalescing already limit how often it reloads, without adding latency. |
| Stop guard defaults to "Keep working"; letters cancel it | Interrupting an agent run is costly, and a stray key must not do it. |
| Mint T3 tokens with the `t3` CLI when possible | No setup for local, loopback and SSH servers. Remote http(s) needs explicit pairing. |
| Remote terminals follow the T3 SSH host | The shell opens where the agent's files are. |
| Vendor patch on termenv | Avoids a 5 s startup stall on terminals that don't answer color queries. See [DEVELOPMENT.md](DEVELOPMENT.md#5-vendor-patches). |
