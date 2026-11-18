# Functional specification

This document covers what aidiff does from the user's side. Everything about reviewing (diff views, annotations, search, themes, review keys) comes from [revdiff](https://github.com/umputun/revdiff), and its own docs describe that in depth. This document summarizes those features only as far as aidiff uses them, and covers aidiff's own features in full.

## 1. What comes from revdiff

From revdiff, aidiff uses a full-screen review with a file tree on the left and the diff on the right. It provides:
- reviewing uncommitted, staged, single-ref and two-ref changes in git, hg and jj, plus `--only` files, `--stdin`, and `--compare-old/--compare-new`
- line, hunk and file annotations, written as markdown to stdout or `-o` on exit (see [INTEGRATIONS.md](INTEGRATIONS.md))
- view modes, search, hunk navigation, a file picker, reviewed marks, themes, vim motions, and remappable keys

The keys used most often:

| Key | Action |
|---|---|
| `j` / `k`, `]` / `[`, `n` / `N` | line, hunk, file |
| `a` / Enter, `A`, `d`, `@` | annotate line, annotate file, delete, list |
| `O` | flush annotations without quitting |
| `R` | reload (drops annotations) |
| `?` | help (lists every key) |
| `q` / `Q` | quit and write output / discard |

## 2. What aidiff adds

| Feature | Section |
|---|---|
| Run in any folder, with no repository needed | §3 |
| Live refresh | §4 |
| Split view with terminal and chat panes | §5 |
| Embedded terminal, local or remote over SSH | §6 |
| Command palette (ctrl+p) | §7 |
| T3 Code: review threads, chat with the agent | §8 |
| Global keys and safe quit | §9 |

## 3. Folder browsing

Run `aidiff` outside a repository, without `--only`, and it browses the folder.

- Only the top level is read at first. Folders appear as `▸ name/` (collapsed) and `▾ name/` (expanded), listed before files.
- Enter or a click toggles a folder. → expands it and ← collapses it. Collapsing a folder also collapses everything inside it.
- Files open as context-only views and can be annotated like any review.
- Symlinks are shown as files and never followed. VCS metadata directories are skipped.
- `--include` / `--exclude` filter the tree. A folder stays visible if an include prefix points inside it.
- The file picker (`P`) searches only folders that have been expanded.
- Live refresh watches the root and the expanded folders.
- Using refs or `--staged` outside a repository is an error.

## 4. Live refresh

The review reloads by itself when its source changes:

| Source | Refreshes when |
|---|---|
| git repository | any file the repository doesn't ignore changes, or HEAD, the index or a branch moves |
| folder | something changes in the root or in an expanded folder |
| T3 thread | an agent run starts or finishes |

Behavior:
- **Annotations and reviewed marks are kept**, unlike revdiff's `R` reload.
- The cursor stays on the same content and the same screen row. The search, the horizontal scroll and the hunks expanded in collapsed mode are kept too.
- A burst of changes refreshes once, 150 ms after it stops.
- The refresh waits while the user is typing an annotation or a search, a prompt or chord is pending, a popup is open, a file the user selected is still loading, a "mark reviewed" is in progress, or a jump to another file is under way. It runs as soon as that is over.
- It does not run for hg, jj, stdin, compare, or `--only` outside a repository.
- `--no-watch` turns it off. If watching can't start, a warning is logged and the review continues.

## 5. Split view

```
┌ files ───────┐┌ app/main.go ─────────────────────┐│ $ go test ./...    │ T3 chat …
│ ▾ app/       ↔│+ 13     run()                     ↔│ ok  ./app   0.3s   │
└──────────────┘└───────────────────────────────────┘│                    │
 host  T3  main.go  +3/-1  hunk 1/2   ✓ 1/3  2 annotations       ? help
```

- Terminal and chat panes open as full-height columns to the right of the review. Each new pane goes furthest right.
- Together the panes take `--terminal-width` percent of the width (default 45, range 10–90). Space is split evenly when a pane opens.
- A pane is refused, with a message, if any pane would end up narrower than 10 columns. The review always keeps at least 20.
- Every separator can be dragged with the mouse. It shows `↔` on its middle row and turns blue while dragging.
- A click focuses the pane under the pointer, and alt+← / alt+→ move focus left / right without wrapping: file tree ↔ diff ↔ pane 1 ↔ pane 2. The review's tree (or markdown TOC) and diff are stops of their own; coming from a pane lands on the diff, coming from the left on the tree. While the review is typing (annotation, search) or shows a popup, the keys skip its panes. alt+← on the review shows the side panel (the projects panel of the selected T3 server, or the history panel for `local`) and focuses it; alt+← again shows the connections panel.
- When a pane's program exits, the pane closes. When the last pane closes, the review gets the full width back.
- The status bar runs along the bottom of the whole window, under the panels and panes. While a chat pane has focus, the right of the bar shows `← activity` (while the input is empty) and `ctrl+p commands`; the chat draws no hint row of its own. It shows the machine name, then the project (the repository or folder name, or the T3 project's title), plus `T3` when the review is a T3 thread.

## 6. Terminal pane

- Opened with ctrl+p → **Open Terminal**, or at startup with `--terminal`. Several can be open at once. A pane closes when its shell exits.
- It runs `--terminal-command` (default `$SHELL`). The command is a shell command line, run by `/bin/sh`, so shell quoting works.
- **Directory:** the reviewed directory. In T3 mode it is the thread's directory, or the project's root. A T3 server reached by URL on another machine has folders that are not this machine's, so the pane starts in aidiff's own directory.
- **Remote:** when the T3 review comes over SSH, the pane opens `ssh -t <host>` on the T3 session's own SSH connection, without setting up the host's port forwards a second time, and runs `--terminal-command` there (default: a login shell) in the thread's directory. ssh can prompt inside the pane.
- It is a full xterm-256color terminal: colors, application cursor keys, bracketed paste, and mouse support for programs that ask for it.
- While it has focus, every key except alt+←, alt+→, ctrl+p and ctrl+q goes to the terminal. While the side panel has something to list, ctrl+b and ctrl/alt+1…9 are taken too (projects or history panel).
- Scrollback is kept but cannot be scrolled yet (see [KNOWN_ISSUES.md](KNOWN_ISSUES.md)).

## 7. Command palette

ctrl+p opens it from anywhere.

| Entry | When shown |
|---|---|
| Open Terminal | always |
| T3: Connect to server… | not connected |
| T3: New thread…, T3: Projects… | connected |
| Diff View… | the run's own review of a git repository is on screen |
| T3: Diff View… | a T3 review is on screen |
| T3: Send annotations to agent | a thread is open |
| T3: Reconnect, T3: Connect to another server… | connected |
| Quit | always |
| T3: `<thread title>`, up to 8 recent (`▸` marks the current one), T3: All threads…, under a **Threads** title | connected |

- **Filtering** is a fuzzy subsequence match, so "opt" finds "Open Terminal".
- **Keys:** ↑/↓ (or ctrl+k / ctrl+n / ctrl+j / tab) move, Enter runs, Esc or ctrl+p closes.
- Some entries continue inside the palette with a filterable list or a one-line prompt.

## 8. T3 Code

[T3_INTEGRATION.md](T3_INTEGRATION.md) covers this in detail. In summary:

- **Connect:** use `--t3 local | ssh://host | https://…`, or the palette at any time. Login is automatic where possible; otherwise pair with `--t3-pair`.
- **Review a thread:** shows the thread's uncommitted changes (default) or its branch changes against the base (`--t3-source branch`). The diff refreshes when the agent finishes a step. Switching threads from the palette saves the current review's annotations to history.
- **Change the diff (git folder):** **Diff View…** compares the uncommitted changes (staged and unstaged), only the unstaged or only the staged ones, the unpushed ones (against the branch's upstream), the branch against the default branch, or against a branch picked from the repository's branches (or a typed ref). A branch is compared from the commit it shares with HEAD, so the review shows what this branch changed. Annotations are kept. Not offered for stdin, a compare pair, a plain folder or `--all-files`.
- **Change the diff (T3):** **T3: Diff View…** compares the uncommitted changes, the unpushed ones, the branch against the default branch, or against a branch picked from the repository's branches (or a typed ref); annotations are kept.
- **Projects panel:** connecting shows a panel on the left listing the server's projects, numbered by title (`▸` marks the one on screen, `●` one whose agent is running). Picking one (↑/↓ and Enter, its number, or a click) hides the panel and switches to the project's **workspace**. ctrl+b shows and hides it, and it closes as soon as focus leaves it for the review or a pane (alt+→, a click elsewhere), as the connections panel does; ctrl+1…9 (or alt+1…9, for terminals that do not report ctrl with a digit) switch projects from anywhere. ctrl+p still lists the current project's threads.
- **History panel:** while `local` is the selected connection, the side panel lists this machine's folders instead of projects: every folder aidiff reviewed (a repository by its root, or a plain folder), most recently opened first as of the start, the run's own folder on top, numbered (`▸` marks the one on screen, the parent folder is dim). The list is kept in `~/.config/revdiff/history.json` (50 folders at most; folders gone from disk are left out). Picking one (↑/↓ and Enter, its number, a click, ctrl/alt+1…9 from anywhere) hides the panel and switches to the folder's **workspace**, as a project does; a folder not seen yet in this run opens the way `aidiff` started there with no arguments would (its uncommitted changes, or every file of a plain folder), with its own Diff View…. A stdin or compare review is not a folder and is not listed.
- **Connections panel:** always hidden; alt+← reaches it past the side panel (straight from the review when there is no side panel), and it hides again once focus leaves it (alt+→, Esc, a click elsewhere). It lists this machine (`local` and its host name), then every T3 server aidiff holds a connection to, then **Connect to a server…**. `▸` marks the selected connection, which the side panel and the palette work with; with no server selected that is `local`. Enter on a server selects it and shows its projects. Enter on `local` selects this machine and shows the history panel; there are no projects for `local`. T3 Code running on this machine is reached through **Connect to a server…** (its `local` entry) and is then listed as a server of its own, `local T3 Code`. Servers stay connected while another connection is active, so going back to one needs no new login.
- **Workspaces:** each project has its own review, chat pane, terminals, pane sizes and focus. The first visit opens its most recently updated thread (or its root when it has none) with a chat. Leaving a project keeps its workspace running in the background: going back brings it as it was, with the thread, cursor, annotations, chat input and pane sizes the user left, and the diff refreshes. Each folder of the history panel is a workspace too, kept the same way, the run's own folder included. A run that reviews no folder (stdin, a compare pair) is not kept: it closes, with the terminals opened with it, the first time another workspace is opened (its annotations go to history). The quit dialog counts the panes of every workspace.
- **Send feedback:** **T3: Send annotations to agent** sends every annotation as one message.
- **Chat pane:** opens automatically when a thread is selected, unless `--no-chat` is given. It provides:
  - the transcript, and a status line such as `Working · 2 subagents · 1 process`
  - queued messages
  - slash commands for approvals, model, effort and mode
  - image attachments
  - an activity view
- **Stop an agent:** Esc or `/stop` asks first, with **Keep working** selected. Typing letters cancels the question and never stops the agent.
- **Manage:** create threads, and create projects by browsing the server's folders.

## 9. Global keys and quitting

These are handled by aidiff before any pane sees them, and they can't be remapped.

| Key | Action |
|---|---|
| ctrl+p | command palette |
| ctrl+b | show / hide the side panel: the T3 projects panel while a T3 server is selected, the history panel while `local` is |
| ctrl+1 … ctrl+9, alt+1 … alt+9 | switch to T3 project 1–9 (while connected) |
| alt+← / alt+→ | focus the panel to the left / right, stopping at either end; the review's file tree and diff count as two panels. Past the leftmost panel alt+← shows the side panel (T3 projects, or the local history), then the connections panel |
| ctrl+q | quit |

**Safe quit:**
- If any pane is open, quitting (ctrl+q, or `q` in the review) asks first. **Cancel** is selected by default.
- In the dialog, y or ctrl+q quits, n or Esc cancels, and ←/→/tab/h/l switch buttons.
- Chat panes add a note: "Closes the chat. The agent keeps working on the server; reopen it from the palette."
- On quit every pane is closed, and its processes get SIGHUP, then SIGKILL after 1 s. After that the review writes its output as usual.

## 10. Main flows

### Local agent loop
1. Run `aidiff --terminal` in the repository, and run the agent in the terminal pane.
2. The agent edits files, and the diff refreshes without moving the cursor.
3. Annotate, then press `O` to flush, or quit to print the annotations for the agent.

### T3 remote loop
1. Run `aidiff --t3 ssh://devbox`. The tunnel and login happen automatically, and the latest thread's diff and chat open.
2. Annotate, then ctrl+p → **Send annotations to agent**.
3. The chat shows `Working…`, and the diff refreshes when the agent finishes.
4. Approve with `/accept`, or stop with Esc and then confirm.
5. ctrl+p → **Open Terminal** opens a shell on `devbox` in the thread's folder.

### Folder or document review
1. Run `aidiff` in any folder, expand folders, and open files.
2. Annotate them and quit. The annotations go to stdout.
