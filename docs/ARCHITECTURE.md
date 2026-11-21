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
                     │  │  │ revdiff review │  │ termpane     │  │ chat.Pane    │  …       │  │
                     │  │  │ (ui.Model)     │  │ PTY + vt     │  │ conversation │          │  │
                     │  │  └───────┬────────┘  └──────┬───────┘  └──────┬───────┘          │  │
                     │  └──────────┼──────────────────┼─────────────────┼──────────────────┘  │
                     │   revdiff renderer        $SHELL / ssh    chat.Backend: t3chat        │
                     │   or t3client.Renderer                       over t3client.Conn       │
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
| Chat pane, a library that knows no agent system | `app/chat` | **aidiff** |
| T3 side of the chat: a `chat.Backend` over a T3 thread | `app/t3chat` | **aidiff** |
| Clipboard | `app/clipboard` | **aidiff** |
| Image files for chat attachments | `app/imagefile` | **aidiff** |
| T3 palette: sessions, thread reviews, chat panes | `app/t3palette` | **aidiff** |
| Wiring | `app/revdiff/t3*.go`, `chat.go`, `split.go`, `watch.go`, `source_setup.go`, `review_model.go`, parts of `main.go` and `config.go` | **aidiff** additions to the upstream root |

Our code inside revdiff packages lives in files of its own; the revdiff files only call into it through one-line hooks:
- `app/ui/refresh.go`: `RefreshMsg`, `CanRefresh()` and the quiet reload's anchor (cursor line, search, horizontal scroll, expanded hunks)
- `app/ui/folder.go` (the `folderBrowser` hooks) and `app/ui/sidepane/foldertree.go` (`FolderTree`, injected through `ModelConfig.NewFileTree` for a plain folder): folder browsing
- `app/ui/identity.go` (`MachineIdentityMsg`, `ModelConfig.MachineName`/`T3Mode`) and `app/ui/chrome.go` (status-bar identity, divider handle): pane chrome
- `app/ui/treedrag.go` and `app/ui/sidepane/scroll.go`: dragging the tree/diff divider, wheel-scrolling the tree
- `app/ui/panefocus.go` (`MovePaneFocus`, `FocusPaneEdge`, both through revdiff's own `togglePane`): the review's tree and diff as stops of the split's focus keys
- `app/diff/folder.go`: `FolderReader`, the lazy folder source
- `app/ui/openfile.go` (`OpenFileMsg`, `FindFiles`, `CurrentFile`; hooked into `Update` and `handleFilesLoaded`): the palette's file search and the file kept across a view switch
- `app/diff/rank.go` (`RankPaths`) and `app/diff/folder_search.go` (`FolderReader.SearchFiles`): ranking files by name, and searching a whole local folder
- `app/diff/gitmeta.go`: `DetectVCS` accepts a `.git` only when it is real repository metadata
- `app/diff/watchdirs.go`: which directories to watch in a git repo

**Rule:** keep fork code in our own packages and keep the hooks into revdiff as small as possible, so upstream merges stay cheap.

## 3. How we use revdiff

From our side, revdiff is three things:

1. **A source of diffs.** Something that implements `ChangedFiles(ref, staged)` and `FileDiff(req)`. The composition root picks one for the mode: a git, hg or jj renderer, a file list, stdin, two-file compare, our `FolderReader`, or our `t3client.Renderer`.
2. **A review model** (`ui.Model`), built from `ui.ModelConfig`. We fill in the config and never change how the model works inside.
3. **Output.** When the program ends we read the annotation store from the model and let revdiff's `finalize` write history and `-o`/stdout.

Because `t3client.Renderer` implements the same interface, a T3 thread opens in the review exactly like a local repository.

Changing what a review compares never touches the model either: it builds a new one. **Diff View…** (`app/revdiff/local_diff.go`) rebuilds the run's git review through `newModel` with other refs (a merge base, `HEAD`, or `--staged`), asking git through `localGit` for the branches, the upstream and the default branch; **T3: Diff View…** (`app/t3palette/diff.go`) rebuilds a T3 review with another `t3client.DiffSource`. Both hand the old model's annotation store to the new one and swap it in with `ReplaceInnerMsg`.

## 4. Startup sequence

`app/revdiff/main.go`, `run()`:
1. Parse options: the revdiff flags plus ours (terminal, chat, watch, T3).
2. Pick the source (`setupSource`, `source_setup.go`) in this order: **T3**, then compare, then stdin, then a plain folder (no repository and no `--only`: **FolderReader**), then revdiff's own VCS detection (`setupVCSRenderer`), which reviews `--only` files outside a repository. In T3 mode the connection is made here, before the full-screen UI starts, so connection errors print as plain text.
3. Build the T3 palette (`newT3Palette`, `t3_palette.go`) right away: from here on it owns the T3 session the source opened, so an error in a later step still closes it.
4. Build the revdiff review model.
5. Wrap the review (`newSplit`, `split.go`): `splitview.New(review, {WidthPercent, OpenOnStart, Start, RefreshMsg, Commands, Quit})`. The review is always wrapped, so ctrl+p works in every mode.
6. Start Bubble Tea with the split's options (`splitProgramOptions`): 120 FPS, for terminal-pane responsiveness, and `splitview.QuitGuard()`.
7. Start the watcher for the source (`src.watch`) and hand it to the palette (`t3p.Start`). A git repository's watcher is set up in the background, so listing ignored trees and walking the worktree never delays the first frame. A T3 thread's watch is the palette's, since it follows the thread on screen; in T3 mode with a thread selected, `Start` also opens the chat pane.
8. On exit, unwrap splitview, close every pane (`closeSplit`), and hand the review back to revdiff's finalization. The palette's `OnScreen()` decides how its annotations are filed, because the user may have switched T3 threads.

## 5. Split view (`app/splitview`)

`splitview.Model` is the root model. It holds the review (`inner`) and a list of right-hand panes. Each pane implements:

```go
type Terminal interface {
    Updates() <-chan struct{}; Exited() bool
    SendText(string); SendKey(uv.KeyPressEvent); Paste(string); SendMouse(vt.Mouse)
    Resize(w, h int); Render(showCursor bool) string; Close()
}
```

`termpane.Pane` and `chat.Pane` implement it. The interface's doc states the contract: `Updates` is closed once the pane is closed (the split's wait on it ends with the pane), and `Close` may block, so the split calls it off the UI goroutine. A pane can also implement `QuitNote() string` (its words in the quit dialog) and `MouseCaptured() bool` (the pane keeps the mouse while dragging); both are named consumer-side interfaces in `terminal.go`.

How it works:
- **The review sees only its own column.** It is told the window is the left column's width, so its layout and click handling are unchanged.
- **Global keys come first:** alt+← / alt+→ move focus left / right without wrapping (in a chat or shell they no longer move by word there; ctrl+← / ctrl+→ still do), ctrl+p opens the palette, ctrl+q opens the quit guard, ctrl+b shows and hides the sidebar, ctrl+tab and alt+s open the session switcher (defaults of `Config.Keys`). All other keys go to the focused pane. For a terminal pane they are re-encoded as terminal bytes (`keys.go`).
- **Modified keys:** `runSplit` turns on xterm's modifyOtherKeys (level 1) for the program's run, so terminals report shift+enter, ctrl+enter and the like instead of a plain key. The split decodes both report forms (`CSI 27;mod;code~` and `CSI code;mod u`, `modkeys.go`): a pane that implements `SendModifiedKey` (the chat, which inserts a line break) gets the key with its modifiers, and everyone else, the review and shells included, gets the key a terminal without the mode would have sent. The switcher's keys (`Keys.Switch`, `Keys.SwitchBack`: ctrl+tab, ctrl+shift+tab) are claimed before either, whatever has focus; with no popup open they would otherwise reach a focused pane, or be taken for tab.
- **Layout:** panes are full-height columns. Together they take `--terminal-width` percent (default 45). A new pane gets an equal share and the others shrink proportionally. Minimum widths are 20 columns for the review and 10 per pane. Every separator can be dragged by its grip column. A pane whose program exits is removed. `Model.Close` closes all panes in parallel and waits for them, including panes closed earlier in the background.
- **Quit guard:** `splitview.QuitGuard()` is a `tea.WithFilter` that turns every unconfirmed `tea.QuitMsg` into a quit request, whoever sent it (the review, a palette command, a batch or a sequence). The request quits at once with no pane open and opens the confirmation dialog otherwise; the dialog lists the shells and every other pane's note.
- **Refresh gating:** a `FilesChangedMsg` marks a refresh as pending. It is delivered as `RefreshMsg` only when the review's `CanRefresh()` is true, which is not the case while the user is typing, a popup is open, a reload (a refresh's included) or a selected file is loading, a mark-reviewed fingerprint is pending, or a cross-file jump waits for its file.
- **Replacing the review:** `ReplaceInnerMsg{Model, Retire}` swaps in a different review; the local **Diff View…** uses it.
- **Workspaces** (`workspace.go`): a workspace is the review with its panes, their sizes and the focus. `SwitchWorkspaceMsg{Key, Model, Replace, Done}` parks the one on screen, panes still running, and brings back `Key`'s, or starts it with `Model`; with `Key` on screen it replaces the review. The unnamed workspace (`Config.Workspace` empty, a local review of no folder: stdin, a compare pair) is closed with its panes instead of parked. Done reports what happened on the UI goroutine, which is where the T3 palette tracks the review on screen (`switched`). Every SwitchWorkspaceMsg moves the workspace it leaves on screen to the front of the split's order of use (`mru`), which the session switcher lists. A parked pane's updates are waited on as before, and one that exits is removed from its workspace. `Close` and the quit dialog cover every workspace's panes. Every message a review's commands produce is tagged with that review's id (`innerMsg`) and delivered to it only, on screen or parked, so a reply that lands after a switch never reaches another review and a parked review never waits forever on a load; Bubble Tea's own messages pass untagged.
- **Session switcher** (`switcher.go`, `switcher_view.go`): ctrl+tab (`Keys.Switch`) or alt+s (`Keys.SwitchAlt`) opens a popup over the workspaces, most recently used first, the one on screen first and the one before it selected; it does not open while another popup is (`modal`), and one coming up closes it. Each move puts the selection on screen through `switchTo`, the switch a SwitchWorkspaceMsg makes without touching the order, so the list stays frozen under the selection; only a commit moves the session to the front, and esc switches back to where it opened. A commit is letting go of the modifier that opened it (`switcherState.held`: ctrl for ctrl+tab and ctrl+shift+tab, alt for alt+s), or, in every terminal, enter, a digit, a click, or any other key, which goes no further. Nothing closes it on a timer. Sessions that close while it is open drop out of the list on the next key or frame, the selection staying on its session or moving to the nearest one. The split knows only keys: `Config.Sessions` (consumer-side, nil-safe; rows then show the key) describes each row (`Describe`: name, machine, path, a busy flag, a note) and hands over the `Done` the program's own switch to it would carry (`Show`), so the palette's bookkeeping (`switched`, `switchedLocal`) follows every preview. A workspace `Show` reports not ready (a T3 review whose connection dropped, is being reconnected, or was replaced by a reconnect) is not shown in passing; chosen, the screen goes back to the origin and `Open` brings it the program's way. aidiff fills it with `t3palette.Palette.Sessions()` (`sessions.go`). The unnamed workspace closes when left, so while it is the origin (`hold`) nothing is previewed and only enter, a digit or a click on a row commits. The split counts each workspace's shells itself (panes without a `QuitNote`). The palette's **Switch session…** opens it with nothing held, so it waits for enter.
- **Key releases** (`keyreports.go`, `modkeys.go`): a terminal's usual encodings report no key release, so while the switcher is open waiting for one (`awaitsRelease`) the split pushes the kitty keyboard protocol's flags disambiguate, report event types and report all keys as escape codes (`CSI > 11 u`), which report presses, repeats and releases, the modifier keys included, and pops them (`CSI < u`) as it closes. Bubble Tea v1 names none of those reports; they arrive as raw sequences, which the switcher alone reads while it takes every key, so the flags never reach the rest of the program. `Update` makes the terminal follow the switcher after every message (`keyReports.follow`), so every way it closes pops, and the shared `keyReports` (one per program, like the terminal) keeps push and pop one for one. The program runs on the alternate screen, whose flags stack is its own, so the pop must come before Bubble Tea leaves it: `QuitGuard`'s filter, which runs before Bubble Tea acts on a message, pops on a quit, a suspend or an external program (`handsOverTerminal`), and the next `Update` pushes again if the switcher is still open when the program is back; nothing pops after `Run` returns, where it would take an entry off the main screen's stack. The sequences are written through `Config.Output`, which aidiff points at the `*os.File` the renderer draws on: one sequence per `Write`, and the file's write lock keeps it whole beside the renderer's frames. The first push asks `CSI ? u`; the answer `CSI ? flags u` is swallowed whenever it comes and remembered, and releases count as reported once it shows event types and all keys. `parseKeyReport` reads xterm's `CSI 27;mod;code~` and the kitty forms (`CSI code;mods:event u`, `CSI 1;mods:event A`, `CSI n;mods:event ~`), lock bits masked; a release, or a modifier key, that arrives after the switcher closed, before the terminal took the flags back, is dropped, and a press is routed as a modified key. A quick tap can let ctrl go before the push takes effect: with releases reported, the next event whose modifiers lack the held one proves it (`letGo`) and commits.
- **Confirmations:** `ConfirmMsg{Yes, No}` asks in the quit dialog's popup. `No` runs on every end but a yes: n, esc or Cancel, a newer ConfirmMsg replacing it, or the popup dropped unseen because the model that asked left the screen. Both are tagged to the asking model like its own commands, so the revert's "possibly open popup" hold on refreshes (`app/ui/revert.go`) ends the moment the user says no; its 15 s expiry stays as a backstop.
- **Palette:** `Config.Commands()` is evaluated when the palette opens, when its filter changes and when the review handles a message while it is open, so its entries follow live state without being rebuilt on every frame. A command can open a picker, a prompt, or a flash message.
- **Sidebar:** `Config.Sidebar` is an optional column left of the review (`sidebar.go`) that the embedding program fills; the split knows nothing of what it lists. While `Available()` reports false, its key passes through to the focused pane. A wrapped model with panes of its own (`paneFocuser`, `panefocus.go`) is walked first: while it has focus the focus keys move between its panes and leave it only at its edge, and focus coming in lands on the pane nearest to where it came from. Shown, the sidebar takes focus and its keys and stays only while it or the outer sidebar has focus (`closeUnfocused`, run after every message), sits first in the alt+← / alt+→ order (alt+← on the review shows a hidden, available sidebar), and the review and panes share the remaining width (`width` is the area right of the sidebar, `winW` the window). ctrl+1…9 (decoded from modified-key reports) and alt+1…9 call `Select(n)` from anywhere. Each of its actions may ask to hide it. aidiff fills it with the T3 projects panel (`t3palette/sidebar.go`) while a server is selected, else with the local history panel (`t3palette/history.go`, folders from `app/localhist`). `Config.OuterSidebar` (`outer.go`) is a second column left of it, shown only while it has focus: alt+← reaches it from the sidebar, or from the review when the sidebar is not available, and moving focus away (keys or a click) hides it. aidiff fills it with the connections panel (`t3palette/connections.go`).
- **Status line** (`footer.go`): a review that has a status bar (`HasStatusLine`/`StatusLine`, in our `app/ui/chrome.go`) keeps its full height and still draws the bar as its last row; the split leaves that row out of the review's column and draws `StatusLine(window width)` across the bottom of the whole window instead. Sidebars and panes end one row above it, and clicks on it do nothing. A focused pane that implements `StatusHints() [][2]string` (the chat, built with `chat.Config.HintsOutside`) has its shortcuts drawn on the right of the bar (the chat gives only `← activity` and the host's keys) (`StatusLine(width, hints)`), keys in the bar's color and labels muted in raw ANSI so the bar's background holds; the review's own status keeps at least 30 columns and hints that do not fit are dropped from the last. `--no-status-bar` turns it off, and the chat then keeps its own hint row.
- **Caching:** the review's view is cached until the review gets a message, so typing in a terminal doesn't redraw the diff.

## 6. Terminal pane (`app/termpane`)

```
 keys ─▶ vt encoder ─▶ emulator input ──▶ pty ──▶ child (shell / ssh)
 child ─▶ pty ──▶ emulator (under mutex) ──▶ notify (≤ 1 per 16 ms) ──▶ Updates()
```

- Uses `creack/pty` and `charmbracelet/x/vt`, with `TERM=xterm-256color`. Scrollback is kept at the emulator's minimum of one line, since nothing shows it.
- Input never waits on the child: keys, pastes and the emulator's replies drain from its pipe into an unbounded queue, and a separate goroutine writes the queue to the pty. A child that stops reading (or a paste larger than the pty buffer) stalls only that writer, never the UI.
- Redraws are capped at one per 16 ms, but the first change after a quiet spell is shown at once. `Render` is cached until the screen changes.
- `Close` sends SIGHUP to every process in the child's session, waits 1 s, then sends SIGKILL. Session members are listed from `/proc` on Linux and from `kern.proc.all` plus `getsid` on macOS (`session_linux.go`, `session_darwin.go`). The child is reaped only after that: its exit is observed without reaping (`waitid(WNOWAIT)` on Linux, kqueue on macOS), so its pid, which is also the session id, cannot be reused while the session is signalled.
- The start command and directory come from `app/revdiff/t3_terminal.go`, at the place `t3palette.Palette.TerminalPlace` names (the review on screen's machine and folder):
  - When that T3 session goes through SSH, it runs `ssh -t host` over the T3 tunnel's ControlMaster socket (`-S`, `ClearAllForwardings=yes`) with `--terminal-command`, by default a login shell, in the thread's directory.
  - Otherwise it runs `--terminal-command` through `/bin/sh` in the reviewed directory, or in aidiff's own when that directory is on another machine.

## 7. Live refresh (`app/watch`)

| Source | Watch |
|---|---|
| git repository | fsnotify on the work tree, skipping ignored directories. Also watches the git dir: HEAD, the index, `refs/heads`. |
| Folder (no repository) | Shallow: the root, plus the folders the user has expanded. The list is updated through `FolderReader.SetDirectoryWatcher`. |
| T3 thread | No filesystem watch. `t3client.WatchThread` fires when a run starts or finishes. |
| hg, jj, stdin, compare, `--only` without a repository | None |

Changes are reported once they have paused for 150 ms (`watch.Config.Settle`), so a burst (a save, a checkout, a build) refreshes once. splitview holds the refresh while `CanRefresh()` is false, which includes while a previous refresh is still loading. The reload keeps annotations, the cursor line, the search, the horizontal scroll and expanded hunks. A watcher that runs out of inotify watches logs one warning. If the watcher can't start, aidiff logs a warning and the review works without live refresh. `--no-watch` turns it off.

## 8. T3 client and palette

[T3_INTEGRATION.md](T3_INTEGRATION.md) has the full detail.
- **`t3client.Conn`:** a WebSocket with a read goroutine, a control-frame writer (acks, pings, interrupts) and a ping goroutine that ends a connection whose server stopped answering. Stream callbacks run on the read goroutine and must not block, so callers hand work off with `go p.Send(...)`; none runs after its subscription returns. The server operations are methods on `Conn`; a `Session` owns its lifetime (and any SSH tunnel).
- **`t3client.Renderer`:** adapts a thread's diff preview to revdiff's renderer interface.
- **Palette commands (`app/t3palette`):** connect, threads, projects, send annotations; saved connections (`saved.go`) and the automatic reconnect of the live session (`autoreconnect.go`). The composition root hands it what it cannot reach itself (`Config.NewReview`, `Config.SaveHistory`, the flags) in `app/revdiff/t3_palette.go`. It reaches servers through its own `Session`/`Server` interfaces, which `*t3client.Session` and `*t3client.Conn` satisfy through a small adapter.
- **Session ownership:** the palette tracks the live session (the one commands use), the session of the T3 review on screen, and the sessions of reviews on their way to the screen in a `ReplaceInnerMsg`. A session none of them uses is closed. The review on screen changes only in `Retire`, at the moment the split swaps models, so a connection that lands while a thread opens never strands a session or leaves the palette pointing at the wrong review. Closing sessions, stopping a refresh and saving history run in the background; `Close` waits for them, and anything that lands after `Close` closes its own session.

## 9. Chat pane (`app/chat`) and its T3 backend (`app/t3chat`)

```
 keys, mouse ─▶ chat.Pane ──Send / Interrupt / Decide / Answer / Dismiss, Command.Run──▶ chat.Backend
                    ▲                                                                       │
                    └──── mailbox ◀── Viewer.Show(Conversation), Viewer.Note ◀──────────────┘
                                                        t3chat.Backend: thread events → Conversation
```

`app/chat` is a chat pane for a coding agent that knows no agent system. It owns the generic chat UX: the transcript (the user's messages, the agent's markdown, its work steps summed up in a line until `/work`, todo lists, plans, failures), the queued messages above the composer, the grapheme-aware composer with image tokens, the slash-command popup, scrolling, mouse selection and copy, the stop dialog, the activity view, and the agent's prompts (an approval, or questions answered one by one). It imports no other aidiff package.

- **`chat.Backend`** (consumer-side, in `chat.go`) is what the pane needs of an agent system: `Subscribe(ctx, Viewer)` shows the conversation until ctx ends; `Send`, `Interrupt`, `Decide`, `Answer` and `Dismiss` carry out the user's requests; `Commands()` adds the backend's slash commands to the pane's own; `CheckImage` turns down an image the backend cannot take.
- **`chat.Conversation`** is an immutable snapshot: title, the agent's state, the running turns, the entries (`UserMessage`, `AgentMessage`, `Plan`, `TodoList`, `Approval`, `Questions`, `Failure`, `Step`), the queued messages, the messages it took over (`Delivered`), the background work (`Activity`) and the settings line. A backend builds a new one for every change.
- **Rendering.** Every frame lays the whole transcript out again, so the messages' rendered rows are cached (`rowcache.go`), keyed by kind, text and width: a scroll or a keystroke re-renders nothing that did not change, and a streaming reply renders again only as its text grows. Each render keeps the rows it used and drops the rest. A wheel notch with its render costs about 0.4 ms on a 200-exchange conversation, against 36 ms without the cache (`BenchmarkPane_WheelScroll`).
- **Commands.** The pane's own (`/help`, `/stop`, `/accept`, `/decline`, `/cancel`, `/answer` and `/1`…`/9`, `/dismiss`, `/attach`, `/work`, `/activity`) win over a backend's of the same name. A backend's `Command.Run` runs with the pane's lock held and returns a `Reply`: a note now, or a `Send` the composer waits on as on a message, with the note once it succeeded; an error turns the input down. A command without `Run` is the agent's own: listed last and sent as a message, like any unknown `/command`.
- **Locking.** One mutex guards the pane. A backend delivers through a mailbox with a lock of its own, so `Viewer.Show` and `Viewer.Note` never wait on the pane: a backend may call them from any goroutine with its own locks held, even from a command's `Run`. The pane takes the newest delivery whenever it takes its lock (`lock()`), so a frame shows what was delivered last. Each subscription has an id; what an older one delivers is dropped, and so are the results of requests, clipboard and file reads (`goLocked`) started before the pane followed another backend.
- **OS concerns are injected.** `Config.Clipboard` and `Config.Files` are consumer-side interfaces with the pane's own `Clip` and `Image` types; the composition root adapts `clipboard.System` and `imagefile.Reader` to them (`app/revdiff/chat.go`). `ImageFiles.Read` asks the backend's check before it reads a file. The host's own shortcuts (aidiff's palette key) reach the hint row through `Config.HostKeys`, so the pane names no key it does not handle itself.
- Rendering changes no state: each frame records its `layout` (page size, selectable rows, scroll limits), which keys and the mouse read.

`app/t3chat` is the T3 layer. `t3chat.Chats` serves the threads of one connection and holds the server's catalog (models, provider commands), which its backends share: it is fetched once, and after a failure again only by a command that needs it. `Chats.Thread(id)` is a `*t3chat.Backend`: it follows `orchestration.subscribeThread`, keeps the thread's items, runs, requests, subagents and provider threads, and shows the pane a new `Conversation` after every event: turn items become entries and steps, the server's queue and the messages it took over are matched by message id, the activity list joins subagents, command rows and the active provider thread's background tasks, and the thread's model, effort, interaction and runtime modes make the settings line. It owns T3's commands (`/model`, `/effort`, `/plan`, `/build`, `/mode`, `/clear`, `/side`, and the provider's from the catalog), the title seed of a thread's first message, image uploads (`assets.persistChatAttachments` before `message.dispatch`) and T3's 10 MB image limit. Stopping sends `thread.stop`; `/clear` asks the palette, through the hook set with `Chats.OnNewThread`, for a new thread in the same project with the thread's settings, which `openMsg` puts on screen and the chat follows (with no hook it arms a fresh provider session instead).

`app/t3palette` wires the two: it opens a `chat.Pane` on the thread on screen and, when the thread changes, points each open pane at a new backend (`Pane.Follow`), keeping the `Chats` of a connection so its catalog is fetched once.

## 10. Design decisions

| Decision | Rationale |
|---|---|
| Wrap the revdiff review instead of modifying it | Upstream merges stay cheap, and the review knows nothing about panes. |
| Fork features live in their own packages | Keeps the same merge benefit, and keeps OS work (PTY, sockets, fsnotify, clipboard) out of the review UI. |
| T3 thread exposed as a revdiff renderer | T3 reuses the whole review UI with no special cases. |
| Quiet refresh separate from revdiff's `R` reload | `R` drops annotations on purpose. An automatic refresh must never lose work. |
| Refresh waits on `CanRefresh()` | The view must not reload while the user is typing or has a popup open. |
| 150 ms settle in the watcher | Without it every event of a burst started a full reload, each one bumping every load sequence while the previous git processes kept running. |
| Stop guard defaults to "Keep working"; letters cancel it | Interrupting an agent run is costly, and a stray key must not do it. |
| The chat pane is a library behind `chat.Backend` | The pane can serve other agent systems; everything T3 lives in `app/t3chat`. |
| Backends deliver through a mailbox | A backend may show the conversation from anywhere, its own commands included, without lock-order rules between it and the pane. |
| Mint T3 tokens with the `t3` CLI when possible | No setup for local, loopback and SSH servers. Remote http(s) needs explicit pairing. |
| Remote terminals follow the T3 SSH host | The shell opens where the agent's files are. |
| Patched termenv in `third_party/` | Avoids a 5 s startup stall on terminals that don't answer color queries. A `replace` survives `go mod vendor`. See [DEVELOPMENT.md](DEVELOPMENT.md#5-patched-dependencies). |
