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
- A click focuses the pane under the pointer, and ctrl+] cycles focus: review → pane 1 → pane 2 → … → review.
- When a pane's program exits, the pane closes. When the last pane closes, the review gets the full width back.
- The status bar shows the machine name, plus `T3` when the review is a T3 thread.

## 6. Terminal pane

- Opened with ctrl+p → **Open Terminal**, or at startup with `--terminal`. Several can be open at once. A pane closes when its shell exits.
- It runs `--terminal-command` (default `$SHELL`). The command is a shell command line, run by `/bin/sh`, so shell quoting works.
- **Directory:** the reviewed directory. In T3 mode it is the thread's directory, or the project's root. A T3 server reached by URL on another machine has folders that are not this machine's, so the pane starts in aidiff's own directory.
- **Remote:** when the T3 review comes over SSH, the pane opens `ssh -t <host>` on the T3 session's own SSH connection, without setting up the host's port forwards a second time, and runs `--terminal-command` there (default: a login shell) in the thread's directory. ssh can prompt inside the pane.
- It is a full xterm-256color terminal: colors, application cursor keys, bracketed paste, and mouse support for programs that ask for it.
- While it has focus, every key except ctrl+], ctrl+p and ctrl+q goes to the terminal.
- Scrollback is kept but cannot be scrolled yet (see [KNOWN_ISSUES.md](KNOWN_ISSUES.md)).

## 7. Command palette

ctrl+p opens it from anywhere.

| Entry | When shown |
|---|---|
| Open Terminal | always |
| T3: Connect to server… | not connected |
| T3: New thread…, T3: Projects… | connected |
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
- **Review a thread:** shows the thread's branch changes (default) or its uncommitted changes (`--t3-source uncommitted`). The diff refreshes when the agent finishes a step. Switching threads from the palette saves the current review's annotations to history.
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
| ctrl+] | cycle focus between the review and the panes |
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
