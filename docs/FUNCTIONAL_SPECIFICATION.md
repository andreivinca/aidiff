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
| `x` / `X` | revert the change under the cursor / the whole file (§8) |
| `?` | help (lists every key; `/` searches it, Esc clears the search) |
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
| Global keys, tabs of the open projects (alt+1…9), session switcher (ctrl+tab) and safe quit | §9 |

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
- A click focuses the pane under the pointer, and alt+← / alt+→ move focus left / right without wrapping: file tree ↔ diff ↔ pane 1 ↔ pane 2. On the Linux text console (`TERM=linux`) the kernel takes alt+← / alt+→ to switch virtual consoles, so they never reach aidiff (the screen jumps to another console's login prompt; alt+→ or alt+F1… comes back); alt+h / alt+l move the focus too, there and everywhere else, and the panels' key help names both pairs. The review's tree (or markdown TOC) and diff are stops of their own; coming from a pane lands on the diff, coming from the left on the tree. While the review is typing (annotation, search) or shows a popup, the keys skip its panes. alt+← on the review shows the side panel (the projects panel of the selected T3 server, or the history panel for `local`) and focuses it; alt+← again shows the connections panel.
- When a pane's program exits, the pane closes. When the last pane closes, the review gets the full width back.
- The status bar runs along the bottom of the whole window, under the panels and panes. The bar is the green of the theme's added lines, with their text color (a fixed green when the review gives none), except the part under the focused panel (a side panel, the review's tree or diff, a terminal or chat pane), which keeps the theme's status bar color (orange by default), so the focused panel stands out; the mark follows focus, window resizes and divider drags (reverse video when the bar has no color). While a chat pane has focus, the right of the bar shows `← activity` (while the input is empty) and `ctrl+p commands`; the chat draws no hint row of its own. It shows the machine name, then the project (the repository or folder name, or the T3 project's title), plus `T3` when the review is a T3 thread.

## 6. Terminal pane

- Opened with ctrl+p → **Open Terminal**. A local folder opens with one, as a T3 thread opens with its chat: the run's own review at startup, and a folder of the history panel the first time it is opened in a workspace (going back to it keeps the panes it had); `--no-terminal` turns this off. `--terminal` opens one at startup with a T3 thread too. Several can be open at once. A pane closes when its shell exits.
- It runs `--terminal-command` (default `$SHELL`). The command is a shell command line, run by `/bin/sh`, so shell quoting works.
- **Directory:** the reviewed directory. In T3 mode it is the thread's directory, or the project's root. A T3 server reached by URL on another machine has folders that are not this machine's, so the pane starts in aidiff's own directory.
- **Remote:** when the T3 review comes over SSH, the pane opens `ssh -t <host>` on the T3 session's own SSH connection, without setting up the host's port forwards a second time, and runs `--terminal-command` there (default: a login shell) in the thread's directory. ssh can prompt inside the pane.
- It is a full xterm-256color terminal: colors, application cursor keys, bracketed paste, and mouse support for programs that ask for it.
- While it has focus, every key except alt+←, alt+→, ctrl+p, alt+p, ctrl+q and alt+s goes to the terminal; alt+h and alt+l are taken too, as focus keys, and alt+1…9 (ctrl+1…9 where the terminal reports them) and alt+w, as tab keys (§9); ctrl+tab and ctrl+shift+tab, where the terminal reports them, open the session switcher instead (§9), and in a terminal that does not they arrive as tab and shift+tab. While the side panel has something to list, ctrl+b and alt+b are taken too (projects or history panel).
- Scrollback is kept but cannot be scrolled yet (see [KNOWN_ISSUES.md](KNOWN_ISSUES.md)).

## 7. Command palette

ctrl+p opens it from anywhere, its query starting with `>`: the commands. Deleting the `>` searches files by name instead; typing `>` again goes back to the commands.

- **Files:** a review of changes lists its changed files first (all of them for an empty query); under a rule titled **In the project** come the other files of its project that match (a local repository's, or a T3 review's folder), and `no changed file matches` stands above the rule when none of the changes does. Picking a project file switches the review to its file tree and shows the file there. The **Project files** view searches every file of the folder, folders not opened included: `git ls-files` (tracked and untracked, ignored ones left out) for a local repository, every file of a plain folder, `projects.searchEntries` on a T3 server. A file name starting with the query ranks first, then one holding it, then a path holding it, then a path holding its characters in order. Each row is the file name with its folder beside it; Enter shows the file, opening the folders above it in the tree. At most 200 are listed.
- **Toolbar** (the review's top row; F10 or alt+m opens it from the keyboard): the Changes / Files toggle, **◧ View ▾** (what to list, then the view toggles), **↕ Whole files ▾** / **↕ N lines ▾** (context), **⇄ Compare ▾** (what the review compares: the same choices as Diff View… / T3: Diff View…, the current one ticked, each with what it compares against on the right; it opens at once saying `loading…` while git or the T3 server is asked, and says why when there is nothing to compare; **Against a branch…** opens the branch picker), and, at the row's right end, **?** (help) and **⋯ ▾** in the corner (**Notes**, the annotation list, and **Reload** where a reload applies, each with its key; then **Copy annotations** and, on a T3 thread's review, **Send annotations to agent**, greyed while there are none).
- **Whole files or changed blocks** are chosen in the toolbar's context dropdown (`↕ Whole files ▾` / `↕ N lines ▾`): **Whole files** on top, then the context sizes, picking one shows the changed blocks with that many lines around each change; Diff View… lists only what to compare and the file tree.
- **Switching views** (the View menu's Changes / Project files, a Diff View… row) keeps the file on screen selected in the new view when that view lists it; a file that is not among the changes leaves the first changed file selected.

| Entry | When shown |
|---|---|
| Open Terminal | always |
| Switch session… | another session is open: opens the session switcher (§9); its detail names ctrl+tab and alt+s |
| Close tab | two or more tabs are open: closes the tab on screen, after a confirmation (§9) |
| Add Project… | `local` is the selected connection: browse this machine's folders (starting at the parent of the folder on screen, else the home folder; a typed path, absolute, `~/…` or relative, goes there) and open one in its own workspace, added to the end of the history panel. `a` in the history panel does the same |
| T3: Connect to server… | not connected |
| T3: New thread…, T3: Add Project… (browse the server's folders and make one a project; `a` in the projects panel does the same) | connected |
| Diff View… | the run's own review of a git repository is on screen |
| T3: Diff View… | a T3 review is on screen |
| T3: Connect to another server… | connected |
| Quit | always |
| T3: `<thread title>`, up to 8 recent (`▸` marks the current one), T3: All threads…, under a **Threads** title | connected |

- **Filtering** the commands is a fuzzy subsequence match on what follows `>`, so ">opt" finds "Open Terminal".
- **Keys:** ↑/↓ (or ctrl+k / ctrl+n / ctrl+j / tab) move, Enter runs, Esc or ctrl+p closes.
- Some entries continue inside the palette with a filterable list or a one-line prompt.

## 8. T3 Code

[T3_INTEGRATION.md](T3_INTEGRATION.md) covers this in detail. In summary:

- **Connect:** use `--t3 local | ssh://host | https://…`, or the palette at any time. Login is automatic where possible; otherwise pair with `--t3-pair`.
- **Review a thread:** shows the thread's uncommitted changes (default) or its branch changes against the base (`--t3-source branch`). The diff refreshes when the agent finishes a step. Switching threads from the palette saves the current review's annotations to history.
- **Change the diff (git folder):** **Diff View…** compares the uncommitted changes (staged and unstaged), only the unstaged or only the staged ones, the unpushed ones (against the branch's upstream), the branch against the default branch, or against a branch picked from the repository's branches (or a typed ref). A branch is compared from the commit it shares with HEAD, so the review shows what this branch changed. Annotations are kept. Not offered for stdin, a compare pair, a plain folder or `--all-files`.
- **Revert:** in a local git review of the working tree (the unstaged changes, or the working tree against a ref such as the uncommitted changes), `x` reverts the change block under the cursor and `X` the whole file on screen, each confirmed in a popup (`y` or Enter on the confirm button; `n`, Esc or Cancel, which is selected first, leave everything as it is). A change is reverted by editing the file: its added lines are taken out and its removed lines put back, refused when the file changed since the review loaded it. A file is restored from what the review compares against, or deleted when it is new (renamed: the old name restored, the new one deleted); against HEAD it is unstaged too. The review then reloads quietly, keeping its annotations. Not offered for staged or two-ref reviews, stdin, a compare pair, a plain folder, or a T3 review.
- **Change the diff (T3):** **T3: Diff View…** compares the uncommitted changes, the unpushed ones, the branch against the default branch, or against a branch picked from the repository's branches (or a typed ref); annotations are kept.
- **Projects panel:** connecting shows a panel on the left listing the server's projects, numbered by title (`▸` marks the one on screen, `●` one whose agent is running). Picking one (↑/↓ and Enter, its number, or a click) hides the panel and switches to the project's **workspace**. ctrl+b shows and hides it, and it closes as soon as focus leaves it for the review or a pane (alt+→, a click elsewhere), as the connections panel does. Its digits pick a project only while it has focus; alt+1…9 pick a tab from anywhere (§9). ctrl+p still lists the current project's threads.
- **History panel:** while `local` is the selected connection, the side panel lists this machine's folders instead of projects: every folder aidiff reviewed (a repository by its root, or a plain folder), most recently opened first as of the start, the run's own folder on top, numbered (`▸` marks the one on screen, the parent folder is dim). The list is kept in `~/.config/revdiff/history.json` (50 folders at most; folders gone from disk are left out). Picking one (↑/↓ and Enter, its number while the panel has focus, or a click) hides the panel and switches to the folder's **workspace**, as a project does; a folder not seen yet in this run opens the way `aidiff` started there with no arguments would (its uncommitted changes, or every file of a plain folder), with its own Diff View…. A stdin or compare review is not a folder and is not listed.
- **Connections panel:** always hidden; alt+← reaches it past the side panel (straight from the review when there is no side panel), and it hides again once focus leaves it (alt+→, Esc, a click elsewhere). It lists this machine (`local` and its host name), then every T3 server aidiff holds a connection to, then **Connect to a server…**. `▸` marks the selected connection, which the side panel and the palette work with; with no server selected that is `local`. Enter on a server selects it and shows its projects. Enter on `local` selects this machine and shows the history panel; there are no projects for `local`. T3 Code running on this machine is reached through **Connect to a server…** (its `local` entry) and is then listed as a server of its own, `local T3 Code`. Servers stay connected while another connection is active, so going back to one needs no new login.
- **Workspaces:** each project has its own review, chat pane, terminals, pane sizes and focus. The first visit opens its most recently updated thread (or its root when it has none) with a chat. Leaving a project keeps its workspace running in the background: going back brings it as it was, with the thread, cursor, annotations, chat input and pane sizes the user left, and the diff refreshes. Each folder of the history panel is a workspace too, kept the same way, the run's own folder included. A run that reviews no folder (stdin, a compare pair) is not kept: it closes, with the terminals opened with it, the first time another workspace is opened (its annotations go to history). The quit dialog counts the panes of every workspace. Each open workspace has a tab on the window's top row, in the order they were opened, and alt+1…9 go to tab 1–9; ctrl+tab (or alt+s) switches between them in the order they were last used (§9). Closing a workspace's tab (its ×, alt+w) closes it with its panes; its annotations go to history.
- **Send feedback:** **Send annotations to agent**, in the toolbar's **⋯** menu while a thread's review is on screen, sends every annotation as one message.
- **Copy annotations** (the toolbar's **⋯** menu): puts every annotation, in the output format, on the clipboard, to paste into an agent in a terminal pane or anywhere else. It uses wl-copy (Wayland) or xclip (X11), and under WSL without either, Windows' `clip.exe`.
- **Annotation list tools:** the `@` popup ends with a row of tools, for local and T3 reviews alike: `c` **copy all** does what Copy annotations does, `D` **delete all** removes every annotation of the review after a confirmation popup (the annotated-only filter goes off with them). A click on a tool does the same.
- **Chat pane:** opens automatically when a thread is selected, unless `--no-chat` is given. It provides:
  - the transcript, and a status line such as `Working · 2 subagents · 1 process`
  - queued messages
  - a side chat (a thread forked with `/side`, here or in T3) carries a colored label naming the thread it was forked from, `side chat · <parent title>` (`side chat` alone when that thread is not listed), after its title in the ctrl+p thread rows and **All threads…**, where typing the label's words finds it, and on its chat pane's title row. T3's own clients list a side chat only under its parent thread, never on its own.
  - an answered question stays in the transcript as `question: …` with its answer under it, `→ …`: the option's label, or the text typed, wherever it was answered (here, the phone, T3's web client), read from the item's `questionAnswer`.
  - while the transcript is scrolled up, output arriving below leaves the view where it is; the status row above the input then says, in the warning color, what is below: `↓ N new messages · End` (the agent's messages, questions, approvals, failures since the view left the end; work steps do not count), else `↓ N lines below · End`. A click on that row, or End, goes to the end. A new question, approval or secret request the agent waits on brings a scrolled view to the end by itself.
  - slash commands for approvals, model, effort and mode; `/side <question>` forks a side chat, which opens in a pane of its own to the right of the others (`/close` in it, or the `×` on its title row, closes that pane, its thread staying on the server; the thread's own chat has neither) and stays on its thread when the review moves to another
  - your message, once scrolled above the top, stays pinned there until your next one pushes it out; a click on it scrolls back to it, whole, with the start of the answer under it
  - the agent's secret requests (a sudo password, a token): typed masked in the composer and handed to T3 Code, never into the chat
  - attachments: files copied in a file manager and pasted with ctrl+v, a pasted path (quoted, escaped, `~/` or `file://`), or `/attach <path>`, each a token naming it in the input (`[File #1: report.pdf]`, an image as `[Image #1]`); files are uploaded to the T3 server when the message is sent (up to 50 MB each), with progress on the message's card and esc to cancel; sent messages list their attachments
  - an activity view
- **Stop an agent:** Esc or `/stop` asks first, with **Keep working** selected. Typing letters cancels the question and never stops the agent.
- **Manage:** create threads, and create projects by browsing the server's folders.

## 9. Global keys and quitting

These are handled by aidiff before any pane sees them, and they can't be remapped.

| Key | Action |
|---|---|
| ctrl+p | command palette (`>` commands; delete it to search files by name) |
| ctrl+b | show / hide the side panel: the T3 projects panel while a T3 server is selected, the history panel while `local` is |
| alt+1 … alt+9, ctrl+1 … ctrl+9 | go to tab 1–9 (ctrl+digit only where the terminal reports it) |
| alt+w (or alt+W) | close the tab on screen, after a confirmation |
| ctrl+tab, alt+s | session switcher: the open sessions, most recently used first; ctrl+shift+tab opens it on the least recently used one |
| alt+← / alt+→, or alt+h / alt+l | focus the panel to the left / right, stopping at either end; the review's file tree and diff count as two panels. Past the leftmost panel alt+← shows the side panel (T3 projects, or the local history), then the connections panel |
| ctrl+q | quit |

**Tabs:** while two or more workspaces (a folder of the history panel, a T3 project) are open, the window's top row is a strip of tabs, one per workspace, above the side panels, the review and the panes, which are one row shorter while it is there. A single workspace keeps the row, and below 10 rows the strip hides.
- Tabs come in the order the workspaces were opened, by any route (the side panels, the palette, Add Project, opening a thread, the session switcher, auto-chat at the start); opening one that is open already goes to its tab, and switching never reorders them, so alt+3 is always the third project. Tabs past the ninth have no number.
- Each tab shows its number, the project's name and its machine (`local`, or the T3 server's host followed by `T3`), e.g. `▌1 aidiff · local × `, with the switcher's marks: `●` an agent working, `$` a terminal open, and `disconnected`, `reconnecting…` or `reconnected` for a T3 project whose connection dropped or was replaced.
- **Colors:** each tab starts with a bar (`▌`) in its machine's color, so the tabs of one machine read as a group, and only the tab on screen is drawn on that color, in bold, so it stands out from the others, which keep the row's background. The color comes from a hash of the machine's name as the tab shows it, out of eight muted ones (blue, green, rose, purple, teal, olive, brown, slate), each with its text color set, so it never changes as tabs open and close, across reconnects or between runs; two machines can share one. The gaps between tabs and the rest of the row keep the terminal's background. The switcher shows each machine's name on the same color. The colors are entries of the 256-color palette; on the Linux console each keeps a color of its own family (some share one there), and `--no-colors` drops them: the tab on screen is then in reverse video and tabs are apart by `│`.
- A click on a tab goes to it; a click on its `×` closes it. Every tab has one. ctrl+p → **Close tab** and alt+w close the tab on screen. Closing always asks first, in a popup, which names what it would lose: a terminal open in it, a chat whose agent is working, or annotations not yet filed in history. Closing files the review's annotations in history, as leaving it does, closes its terminals and chat (the agent keeps working on the server), and the run forgets it: opened again, it starts anew, in a new tab at the end. Closing the tab on screen goes to the tab on its right, else its left (one whose connection can come back as it was, when there is one). The last tab stays: closing it says so.
- A workspace whose connection dropped keeps its tab; going to it reconnects or rebuilds it as the switcher does.
- When the tabs do not fit, names are shortened first; then the strip shows the tabs around the one on screen, with `‹` and `›` where tabs are cut off, and a click on either goes to the first tab hidden beyond it.
- A run of no folder (stdin, a compare pair) has no tab: it closes when another workspace opens, so it never stands beside one.

**Session switcher:** every workspace (a folder of the history panel, a T3 project) is a session. ctrl+tab opens a popup listing the open ones, most recently used first, the way alt+tab lists windows: it stays while ctrl is held, and letting go switches, where the terminal can say when a key is let go.
- The session on screen is row 1, marked `▸`; row 2, the one used before it, is selected, so ctrl+tab and letting go of ctrl (or ctrl+tab and enter) goes back to it. With only one session open, `no other session open` shows on the bottom row instead.
- Each row shows the session's name (a T3 project's title, a folder's name), its machine (`local`, or the T3 server's host followed by `T3`), and dimmed where it lives (the folder it is in, or the project's folder when the title is another name), cut from the left to fit. `●` marks a T3 project whose agent is working (known for the selected server's projects only, from the listing the projects panel uses), `$` one with a terminal open; a legend names the glyphs present. Rows 1–9 are numbered.
- ctrl+tab, tab, ↓ or j moves down, ctrl+shift+tab, shift+tab, ↑ or k moves up, both wrapping, and alt+s / alt+S do the same. The selected session comes on screen at once, behind the popup.
- **Letting go of the modifier that opened it** switches to the selection: ctrl for ctrl+tab and ctrl+shift+tab (shift alone going up changes nothing), alt for alt+s. This needs a terminal that reports key releases, which aidiff asks for through the kitty keyboard protocol while the popup is open, and only then: kitty, Ghostty, foot, WezTerm (with `enable_kitty_keyboard = true`), Alacritty and iTerm2 speak it. The hint at the bottom says `release ctrl to switch` once the terminal has answered that it does, `enter to switch` otherwise. Keys pressed with the modifier still held are the keys themselves (ctrl+j moves down, ctrl+3 picks row 3), and keys let go other than the modifier change nothing.
- In every terminal, Enter, the row's digit, a click on a row, any other key (which then does nothing else) or a click outside the popup switches to the selection, and Esc goes back to the session the popup opened on. Nothing closes it on a timer: in a terminal that reports no releases (tmux, GNOME Terminal, Konsole, Terminal.app, VS Code's and T3 Code's terminals, xterm) it stays open until one of these.
- A quick tap of ctrl+tab can let go of ctrl before the terminal has started reporting releases, so no release ever comes. Every key the terminal reports after that says which modifiers are down, so the next one (even a tab let go) shows that ctrl is up and switches; esc and a digit still do what they say. Until that next key or click the popup stays.
- The order changes only when a switch is made, never while the popup is open. Every other way of switching (the side panel, a tab or alt+1…9, the palette, opening a thread or a folder) moves the session switched to to the front too, and a new session starts at the front. A closed tab's session is gone from the list.
- A T3 session whose connection dropped (`disconnected`), is reconnecting (`reconnecting…`) or was reconnected while it was in the background (`reconnected`) says so on its row and is not shown in passing. Chosen, the screen goes back to where the popup opened while it opens: a reconnected one is rebuilt over the new connection, a reconnecting one comes and is rebuilt once the reconnect lands, a disconnected one becomes the selected connection, which starts the reconnect, as selecting it in the connections panel does.
- A review of no folder (stdin, a compare pair) closes when left, so it never leaves in passing: nothing is shown before a choice, and only Enter, a digit or a click on a row leaves it; letting go of ctrl changes nothing, and Esc, another key or a click outside stays. (It is the only session of such a run until another one is opened, which closes it, so in practice the switcher says `no other session open` there.)
- It does not open over the palette or another popup, and another popup coming up closes it. The palette's **Switch session…** opens it too; with no modifier held it waits for Enter.
- ctrl+tab and ctrl+shift+tab reach aidiff only in a terminal that reports modified keys (aidiff asks for xterm's modifyOtherKeys) and does not keep them: Ghostty, kitty, WezTerm and Windows Terminal switch their own tabs with them by default, and tmux passes them on only with `extended-keys`. alt+s works in every terminal. To free ctrl+tab: Ghostty `keybind = ctrl+tab=unbind`, kitty `map ctrl+tab no_op`, WezTerm `DisableDefaultAssignment`, Windows Terminal `"command": "unbound"`, tmux `set -s extended-keys on` (likewise ctrl+shift+tab). In a terminal that reports neither, tab stays tab.

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
2. Annotate, then **⋯** → **Send annotations to agent** in the toolbar.
3. The chat shows `Working…`, and the diff refreshes when the agent finishes.
4. Approve in the picker under the request (↑/↓ chooses Accept or Decline, Enter on an empty input confirms) or with `/accept`, or stop with Esc and then confirm.
5. ctrl+p → **Open Terminal** opens a shell on `devbox` in the thread's folder.

### Folder or document review
1. Run `aidiff` in any folder, expand folders, and open files.
2. Annotate them and quit. The annotations go to stdout.
