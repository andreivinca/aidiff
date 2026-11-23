# T3 Code integration

[T3 Code](https://github.com/pingdotgg/t3code) is a server that runs coding agents in threads, one thread per project and conversation. aidiff can act as a T3 client. It shows a thread's changes in the review UI, chats with the agent in a pane, and opens terminals where the agent runs.

The code lives in `app/t3client` (protocol and connection), `app/t3chat` (the T3 backend of the chat pane), `app/t3palette` (palette commands and the sessions they own), and `app/revdiff/t3*.go` (wiring and terminal start-up). The chat pane itself is `app/chat`, a library that knows nothing of T3: `t3chat` turns a thread's events into the conversations it shows and carries out what the user asks (see [ARCHITECTURE.md](ARCHITECTURE.md#9-chat-pane-appchat-and-its-t3-backend-appt3chat)).

## 1. Ways in

| Entry | Effect |
|---|---|
| `aidiff --t3 local` | Connect to the local server and review the most recently updated thread. |
| `aidiff --t3 ssh://devbox` | Same, through an SSH tunnel. |
| `aidiff --t3 https://host:port` | Direct connection. |
| `aidiff --t3-pair '<link or code>'` | Pair first. A link's origin becomes the server if `--t3` is not given. |
| `--t3-thread ID` | Pick a thread by id or id prefix. |
| `--t3-source branch` | Review the thread's branch range against its base (unpushed commits included) instead of its uncommitted changes. |
| `aidiff --t3-threads` | Print `id  idle\|running  project  branch  title` per thread and exit. |
| plain `aidiff`, the last run ending on a T3 project's tab | That project, on screen while its server is dialed (`connecting…`), then its saved thread (else its most recent one, else its root) with its chat. See **Restored tabs** below. |
| ctrl+p → **T3: Connect to server…** (any mode) | Lists `local` (if a runtime file exists) and servers with a saved login, each marked `online` or `offline` once a quick check answers (for `ssh://` that check is an SSH login reading the T3 runtime state; for a URL, any HTTP answer from `/api/auth/session`). `~/.ssh/config` hosts without a saved login follow under their own heading and are not checked: T3 Code must be installed there. Text typed without `://` is taken as `ssh://<text>`. A pasted `t3 pair` link (`https://host:port/pair#token=…`) pairs with that server and saves its login, like `--t3-pair`. |

**Projects panel.** Once a server is connected (from the palette, which shows the panel at once, or with `--t3`, where it starts hidden) ctrl+b shows a panel on the left with the server's projects, numbered 1… by title. Picking a project (Enter, its digit while the panel has focus, or a click) hides the panel and puts the project's workspace on screen: each project of a server (keyed by server label and project id, so a reconnect finds it again) is a splitview workspace with its own review, chat and terminals. A new one opens the project's most recently updated thread with its chat, or the project's root when it has no thread. A thread opened from ctrl+p goes to its project's workspace. A parked workspace keeps its session open and stops its live watch; going back refreshes it. A local review of a folder is a workspace of its own too (history panel, below); only a local review of no folder (stdin, a compare pair) is the unnamed workspace, closed on the first switch. The leaving review is saved to history as a safety net, and each review keeps its annotation store for the run (`app/t3palette/projects.go`). ctrl+tab (or alt+s) switches between the open workspaces, most recently used first, across servers and local folders (`splitview` session switcher; letting go of ctrl switches where the terminal reports key releases, enter elsewhere); `Palette.Sessions()` (`app/t3palette/sessions.go`) describes each one (title, server machine with `T3`, folder, `●` while the selected server's listing shows one of its agents running) and switches with the `Done` the sidebars use (`switched`, `switchedLocal`). A parked review over a connection that dropped, is reconnecting, or was replaced by a reconnect (`stale`) is not previewed while the user moves past it; chosen, a replaced one is rebuilt over the new session (`rebuildParked`, as `openProject` does), a reconnecting one comes on screen and is rebuilt when `takeOver` lands, and a dropped one becomes the live connection (`useLive`), which starts the reconnect, the status bar naming its machine. The panel is `t3palette.Palette.Sidebar()`, shown in splitview's generic sidebar slot; while no server is live the same slot shows the history panel instead.

**Tabs.** Each open workspace also has a tab on the window's top row (`splitview` tab strip; shown from two workspaces up), in the order they were opened, named and colored by `Palette.Sessions()` (its `Peek`, which is `Describe` without fetching the listing again: the strip is drawn on every frame, so its `●` follows the listing as the switcher, the projects panel or a command last fetched it): two projects of one name on two servers are two tabs, told apart by their machine (`<host> T3`), which also picks the tab's color, the same for every tab of that machine. alt+N (ctrl+N where the terminal reports it) goes to tab N the way the switcher does, a dropped or replaced connection included. Closing a tab (its `×`, alt+w, ctrl+p → **Close tab**) asks first when `CloseNote` reports annotations not yet filed in history (the palette's `filed` record of what each store last filed), when a terminal is open there, or when its chat's agent is working; then `Closed` files what the review leaves (`unfiledLocked`, so nothing is filed twice), forgets the parked review or folder, its chat panes and the annotation store the run kept for it (`forgetStoreLocked`), and closes the sessions nothing uses any more. A tab closed on screen first hands the screen to its neighbor through that neighbor's `Done`, with `LeftKept: false`, so `leaveLocked` files and drops it as it does the unnamed workspace. Opening the project again builds a new workspace with no annotations, in a new tab at the end.

**Restored tabs.** `t3palette/restore.go`. At exit `Palette.SaveTabs` turns the split's tabs into what reopens each (`Tab`): a folder's path, or a project's server (`Session.Label`, the spec a saved connection dials), machine, id, title and the thread on screen in its workspace; a tab restored but never opened is saved as it was loaded. The composition root writes them to `~/.config/revdiff/tabs.json` (`localhist.Tabs`: versioned JSON, 50 tabs, flock beside it waited for 2 s at most, atomic 0600 write; the last process to exit wins). A run that starts in a named workspace reads them back in `New` (`initTabsLocked`: each server spec named as a session to it would be, `t3client.ServerLabel`, so the workspace key matches the one a review over that session lands in) and hands the order to the split (`StartTabs` → `splitview.Config.Tabs`), its own workspace in its saved place or appended; the tab the last run showed seeds the switcher's order (`Config.Recent`). Each restored tab is a split workspace not opened yet; `Peek` describes it from what was saved (`not connected` while no connection to its server is open, `connecting…` while one is being made), `Show` reports it not ready, and `Open` opens it: a folder through `LocalConfig.Open` (`folderMsg`, as the history panel opens one, after checking off the UI goroutine that it is still a folder it can list), a project over the open connection to its server, else one made now (`tabConnection`: a choice, as a connect from the connections panel, so the panels show the dial; `adoptTab` keeps it as every connection the user makes and makes it live unless another choice came meanwhile, moving to it what came over a dropped connection to that server). Restored tabs of one server wait for the dial in progress (`tabDials`) instead of dialing again. Once connected, `restoredOn` opens the saved thread when the project still lists it, else the project's most recent thread, else its root, through `openMsg`/`workspaceMsg`, so the chat comes along as it does for `openProject`. A project gone from the listing, an unreadable server spec or a folder gone closes the tab (`dropRestored` → `splitview.DropTabMsg`, with a flash); a dial that fails leaves it, its reason flashed and shown in the panels, and showing it again dials again. `Start` checks the restored folders in the background (`pruneTabsLocked`, never before the first frame) and drops those gone.

A run that names no target and whose saved tab on screen was a T3 project starts on it (`Config.StartTab`, chosen by `app/revdiff/start.go`): `initStartTabLocked` makes it the workspace on screen, restored and not opened, `Opening` tells the split to show its stand-in and open it from its `Init`, and `Open` dials as for any restored tab, the stand-in and the tab saying `connecting…` meanwhile. The review lands in place of the stand-in (`openInPlace`: `Created` with nothing left, which `switched` takes as any opening), with its chat, and with `--terminal` a terminal after it (`withStartTerminal`, in sequence, so it opens in the project's folder). A server that does not answer leaves the stand-in saying `not connected`; Enter on it, or its tab, opens again, one opening at a time (`opening`, `settled`: a second request while one runs only says so, rather than build a second review and chat). A project found gone once connected closes its tab (`dropRestored`), the split going to the neighboring tab; when no other workspace is left, the working directory opens instead (`StartTab.Fallback`), as a run with no saved tabs opens it. Opening one by any other route (the panels, a palette thread row) fills its tab the same way: `switched` and `switchedLocal` forget it as restored. `Closed` forgets one closed before it was opened.

**History panel.** `t3palette/history.go`: while `local` is selected (no live server), `Sidebar()` shows this machine's folders, from `Config.Local` (`LocalConfig`). The list (`app/localhist`, `~/.config/revdiff/history.json`) is read once at start, folders gone from disk dropped and the run's folder (`LocalConfig.Start`, from `sourceSetup.historyDir`: a repository root or a plain folder) put on top, so the numbers stay put while the run lasts; opening one records it as opened now (`Remember`). Each folder is a split workspace keyed `localKey(dir)`. A new one is built by `LocalConfig.Open` (`app/revdiff/local_history.go`: the run's options without its refs, `--only`, stdin, compare, T3 or `--annotations`, through `setupFolderSource`), with its own Diff View… (`localDiffs`). `switchedLocal` is `switched`'s counterpart: it parks the workspace left (a T3 review keeps its session), stops the old live refresh and starts the folder's (`LocalReview.Watch`), and files the annotations left by folder (`LocalConfig.SaveHistory`). `Palette.LocalDir()` names the folder on screen, for Diff View… and for the history filed at exit.

**Connections panel.** `t3palette.Palette.Connections()`, in splitview's outer sidebar slot, reached with alt+← past the projects panel. It lists this machine (`Config.LocalName`, the host name) and the servers the palette holds a session to, one row per server label. Using one (`useLive`) makes it the live session, clears the cached listing so the projects panel fetches that server's projects, and updates the status bar's machine. The `local` row is this machine: using it selects no live server (`useLocal`) and shows the history panel. T3 Code on this machine is connected to through the connect row and listed as `local T3 Code`, apart from the `local` row. `▸` marks the live server, or `local` while there is none. Every server connected to is saved in `~/.config/revdiff/connections.json` (`app/localhist.Connections`, same locking as `history.json`, `Config.Saved`), so later runs list it, greyed, after the connected ones; Enter on a saved row connects to it. `x` forgets the server under the selection (a saved or a connected one, not `local`) after a confirmation popup; a connected one stays connected until the run ends. Every server the user connected to stays open (`Palette.connected`, one per label; a reconnect replaces its predecessor) until the run ends.

In T3 mode the connection is made **before** the full-screen UI starts, so SSH and auth errors print as plain text. The chat pane opens on its own when a thread is selected, unless `--no-chat` is given. The status bar shows the machine name, the T3 project and `T3`, and the info popup gets a `**T3 Code**: <label>` header with the cwd and thread id.

## 2. Connecting

### Server discovery
- **local:** reads `$T3CODE_HOME/userdata/server-runtime.json` (default `~/.t3`), which holds `{pid, host, port, origin}`, and checks that the PID is alive.
- **ssh://host:**
  1. The host is validated against an allowlist: letters, digits and `. _ @ : -`, not starting with `-`.
  2. One `ssh -- host` login reads the remote runtime file and checks the PID on the remote side (exit 3: no state, exit 4: not running).
  3. The client creates a private (0700) temp directory and starts `ssh -N -L <dir>/t3.sock:<remote host>:<remote port>` with `ExitOnForwardFailure=yes` and `StreamLocalBindUnlink=yes`, in its own process group. That ssh is also a ControlMaster on `<dir>/ssh.ctl`, so remote terminal panes share its connection (see §6). Only this user can create a socket there, so the bearer token cannot reach another local process the way a reused TCP port could.
  4. It waits up to 15 s for the socket to accept connections. If ssh exits first, its stderr is reported.
  5. The tunnel lasts for the whole session; closing the session kills ssh and removes the directory. ssh runs under a small `/bin/sh` watchdog whose stdin is a pipe from aidiff: when aidiff ends without closing the session (a crash, `kill -9`), the kernel closes that pipe and the watchdog kills ssh, so the tunnel and the terminals sharing it never outlive aidiff. The exit itself is bounded: once the screen closes, aidiff has 20 s to finish; past that it saves every goroutine's stack to a new `$TMPDIR/aidiff-shutdown-*.txt`, whose name it prints, and exits, which ends the tunnel; with the exit code the review already settled on (10 for annotations under `--exit-code-on-annotations`) once the annotations are written, else 1. A SIGHUP or SIGTERM (the terminal window closing, `kill`) quits without the "Quit aidiff?" dialog, which nobody could answer, and starts the same deadline; a second signal exits at once.
  Every ssh command runs with `BatchMode=yes`, `ServerAliveInterval=15` and `ServerAliveCountMax=3`, so a dead link is noticed within about 45 s.

  **Interactive login** (`app/t3client/login.go`). A connect from the palette (picker or connections panel) that fails with an SSH auth error (`Permission denied`, `Host key verification failed`, `Too many authentication failures`) hands the screen to `ssh -f -N` with `ControlMaster=yes`, `ControlPersist=60` and a `ControlPath` in a per-user 0700 directory (`$TMPDIR/aidiff-ssh-<uid>`, else a fresh temp one), headed `aidiff: log in to <host> for T3 Code`. The user answers ssh's own prompts (password, code, host key); ssh's messages go to a log shown only if it fails. Once it is in the background the connect is retried once, without offering another login. From then on every ssh command to that host adds `ControlMaster=no` and that `ControlPath`. The tunnel does not run its own ssh: the login's connection forwards the socket (`ssh -O forward -L`, cancelled with `-O cancel` when the tunnel stops), and an ssh session over it running `cat >/dev/null`, its stdin aidiff's lifeline pipe, ties the tunnel to the connection. That session ends when aidiff closes the pipe or dies, leaving nothing on the host, and the login exits 60 s after its last session, so it can serve an aidiff restarted within that minute. Terminals share the login's connection. Restored tabs and reconnects never prompt; they fail as before and the user picks the connection again. The remote must run a POSIX shell, as the runtime-state script already requires.
- **http(s)://:** used as given.

### Authentication
The client tries tokens in this order:
1. `--t3-token` / `REVDIFF_T3_TOKEN`.
2. A `--t3-pair` exchange: `POST /oauth/token`, an OAuth token exchange with `subject_token_type=urn:t3:params:oauth:token-type:environment-bootstrap`, `client_label=aidiff` and `client_device_type=bot`. Redirects are never followed, so the credential cannot be carried elsewhere. The token is saved afterwards.
3. A token saved earlier in `t3-tokens.json`.
4. A freshly minted token. This is allowed only for local, loopback and SSH servers. aidiff runs `t3 auth session issue --label aidiff --ttl 30d --token-only --scope orchestration:read --scope orchestration:operate --scope filesystem:read`, locally or over SSH (adding `~/.local/bin` and `~/.t3/bin` to PATH).

If a saved token is rejected, the client mints one new token and retries once. If it can't mint, the error tells the user to run `t3 pair` and pass `--t3-pair`.

A token that cannot be saved (`t3-tokens.json` unwritable) does not fail the connection: the session keeps it and reports a notice (`Session.Notices`). A plain `http://` server that is not on loopback also gets a notice, since the token travels unencrypted. Notices are logged to stderr when the run connects at startup, and flashed on the bottom row after a palette connect or reconnect. Saves to `t3-tokens.json` take an exclusive `flock` on `t3-tokens.json.lock`, and its directory is kept at 0700.

`Session.Redial` connects again with the spec and options the session was made with; after a pairing it uses the token the pairing produced, since the credential is spent.

## 3. Protocol

T3 serves **Effect RPC over a WebSocket**. aidiff implements the part it needs: unary calls, server streams with acks, and pings.

| Aspect | Value |
|---|---|
| URL | `ws(s)://<origin>/ws?orchestrationProtocol=2` |
| Auth | `Authorization: Bearer <token>` on the upgrade request |
| Request | `{"_tag":"Request","id":"<n>","tag":"<method>","payload":…,"headers":[]}` |
| Responses | `Chunk` (`values[]`) and `Exit` (`Success` or `Failure` with an Effect cause) |
| Flow control | `{"_tag":"Ack","requestId":…}` after every chunk |
| Cancel | `{"_tag":"Interrupt","requestId":…}` when the caller's context ends |
| Keep-alive | `{"_tag":"Ping"}` every 25 s; a ping left unanswered for 50 s (no inbound frame of any kind) ends the connection |
| Server failure | A `Defect` frame ends the connection. A frame for a pending request that cannot be decoded, or has an unknown tag, fails that request. |
| Writes | A request is written under the caller's context. Acks, pings and interrupts queue for a writer goroutine of their own (10 s per frame), so the read loop never waits on the socket. |
| Errors | 401/403 → unauthorized. 426 → protocol mismatch ("update one of them"). |
| Max frame | 256 MiB (a diff preview arrives in a single frame) |

### Methods used
| Method | Purpose |
|---|---|
| `orchestration.subscribeShell` | Projects and threads snapshot (`FetchShell`), and run start/finish notifications (`WatchThread`) |
| `orchestration.subscribeThread` | The full transcript stream for the chat pane |
| `review.getDiffPreview` | File list (`metadataOnly`) and per-file diffs. `sourceKind` is `branch-range` or `working-tree`; a branch range takes the `baseRef` **T3: Diff View…** chose, else the server compares with the default branch. |
| `vcs.listRefs` | The repository's branches, local and remote, for **T3: Diff View…** (`refKind: all`, up to 200) |
| `orchestration.launchThread` | New thread, with the caller's `runtimeMode` (the palette sends `full-access`), `interactionMode: default` and workspace root. |
| `orchestration.dispatchCommand` | Every thread command (below), each with a UUIDv4 `commandId` |
| `server.getConfig` | Provider catalog: models, capabilities, slash commands, default model |
| `assets.persistChatAttachments` | Image uploads (data URLs, images only) |
| `attachments.createUploadUrl` | A signed address for one file upload: `{type: file, name, mimeType, sizeBytes}` → `{attachmentId, relativeUrl, expiresAt}` |
| `attachments.delete` | Deletes an upload no message took (`{attachmentId}`; the server deletes only uploads still pending) |
| `secrets.answerRequest` | Answers the agent's secret request (a `secret_request` turn item, from `request_secret`): `{threadId, turnItemId, answer: {type: save, secret} \| {type: decline}}`. T3 stores the secret for the agent; it never enters the thread. |
| `filesystem.browse`, `projects.mutate` | Browse server folders and create projects |

Commands sent through `dispatchCommand`:
- `message.dispatch`, with `dispatchMode: queue_after_active`, plus `titleSeed` and `freshContext` when set
- `runtime-request.respond`
- `thread.user-input.dismiss`
- `thread.stop` (the stop dialog), and `run.interrupt` for a server without it
- `queue.resume` (sent after a stop when messages are queued)
- `thread.interaction-mode.set`
- `thread.fork` (side chat)
- `thread.model-selection.set`
- `thread.runtime-mode.set`

### Stream model
A thread stream starts with a `snapshot` (messages, runs, runtime requests, turn items, subagents, provider threads), followed by `event`s:
- `message.updated`, `turn-item.updated`
- the `thread.*` events whose payload is the whole thread (`thread.created`, `thread.metadata-updated`, `thread.runtime-mode-updated`, …; the list is `threadEventTypes`). Other `thread.*` events, such as `thread.pull-request-link-updated`, are ignored.
- `run.created`, `run.updated`
- `runtime-request.updated`, `subagent.updated`, `provider-thread.updated`

The server re-sends whole entities, so streaming text arrives as the same item with more text. A run counts as active while it is preparing, queued, starting, running or waiting.

### Reconnection
**Reconnecting is automatic** (`t3palette/autoreconnect.go`). Every connection the palette makes is watched through `Session.Done()` (`t3client.Conn.Done`). When the live one ends without the palette closing it, the bottom row says so and the palette redials the server the way it was reached (`Session.Redial`), waiting 1 s, 2 s, 5 s, 10 s, then 30 s between tries, each failure reported, until it answers, the palette closes, or the user makes another connection live. The new session then takes the old one's place on the UI goroutine (`splitview.RunMsg` → `takeOver`): the thread is re-resolved, the review rebuilt with its annotations, the thread watch restarted, and open chat panes pointed at the new connection. A connection in the background is not redialed. While the link is down the chat header shows `disconnected: <error>`.

## 4. Reviewing threads

`t3client.Renderer` implements the review's `Renderer` interface, so everything in [FUNCTIONAL_SPECIFICATION.md](FUNCTIONAL_SPECIFICATION.md) works on T3 diffs. File diffs are fetched one at a time, when opened, once per listing however many views ask at the same time; a fetch that finishes after a reload never lands in the new listing. A preview that lacks the requested source kind is an error, not a fallback to another kind. `WatchThread` sends a refresh when a run starts or finishes, so the diff follows the agent.

Switching thread from the palette:
1. Builds a new review with an empty annotation store.
2. Saves the old review's annotations to history, filed as `<server>:<folder>` (e.g. `ssh://devbox:/srv/wt`) under the folder's name.
3. Restarts the watch, unless `--no-watch` is given.
4. Points open chat panes at the new thread. If none is open and auto-chat is on, a chat pane opens.

**T3: New thread…** gets the default model, launches the thread, and waits for it to appear, polling up to 5 times, 300 ms apart. **T3: Projects…** → **New project…** browses the server filesystem (`..`, typed paths, "Use this folder") and creates a project named after the folder.

**Send annotations to agent** (the toolbar's **⋯** menu, on a thread's review) sends the formatted annotations as a message that starts with `Review annotations from aidiff:`.

**T3: Diff View…** changes what the review on screen compares; its detail names the current choice. It offers:
- **Uncommitted changes**: the working tree.
- **Unpushed changes**: the branch against its upstream (`origin/<branch>`), uncommitted included. Listed only when the branch has an upstream.
- **Branch changes**: the branch against the default branch, uncommitted included.
- **Against a branch…**: every branch but the current one, local and remote; a typed ref (a tag, a commit) is taken too.

The review is rebuilt in place with its annotations, and the info popup names the diff. The choice applies to the reviews of that folder, so threads in other folders compare what `--t3-source` says. A T3 project whose folder is no git repository (`vcs.listRefs` answers `isRepo: false`) has nothing to choose; the command says so.

## 5. Chat pane

The pane (`app/chat`) owns the layout, the composer, the popup, the stop dialog, the activity view and the prompts; the T3 backend (`app/t3chat`) owns what they show of a thread and the commands marked *T3* below.

### Layout
```
 Thread title · Working                              ↑12 · end jumps down
 ───────────────────────────────────────────────────────────────────────
 you: please add tests
 assistant: …
 + Ran 3 commands and changed 1 file · /work shows them
 ✻ checking whether the test covers the empty case
 ┌ Queued ───────────────────────────┐
 │ also update the docs              │
 └───────────────────────────────────┘
 Working · 2 subagents · 1 process
 > _                                         enter queue · / commands
```

**Secret requests.** When the agent asks for a secret with `request_secret` (a sudo password, a token), T3 adds a `secret_request` item that waits for the user. The transcript shows `🔑 the agent asks for the <label>` with its reason, and the composer turns into a masked field, `🔑 <label>: ••••`, until the request ends: Enter hands the secret to `secrets.answerRequest`, Esc declines, ctrl+u clears what was typed, Backspace deletes a character, and pasting adds to it. What is typed is never shown, kept in the input history or sent as a message, and is forgotten once sent. An empty Enter only says what to do. Once the request is answered, declined or times out (`cancelled`), its line says so and the composer takes messages again.

**Queued messages** above the composer are drawn in the muted grey of their `Queued` row, on the panel's background in vertical stripes a cell wide (the panel's shade and one barely lighter), with a blank row between two of them, so they read apart from the sent messages in the transcript.

**Sticky message.** Once one of your messages scrolls above the top of the transcript, it stays pinned there, over the rows below it, while you read the turn that answers it. The next message of yours, coming up under it, pushes it up a row at a time, as CSS's `position: sticky` keeps a heading within its section, and takes its place once it reaches the top, so a message is never hidden under the one before it. A click on the pinned message scrolls back to it: it shows whole, in its place, with the start of the answer under it (when it is taller than the pane, its end and that start). It shows its text lines without the panel's padding, with a blank row under it. A message of more than four lines is compact while its end is out of view: its first three lines and a `…` row. Scrolling back up to it brings its end in under the `…`, one line per row as its lines really come into view, until the gap closes and the whole message shows; then it is back in its place. A pane too short to keep three transcript rows below it shows none.

### State and status
- **Thread state** (backend):
  - `waiting for you` when there is a pending approval, question or secret request, or the latest run is waiting
  - `running` when the latest run is active
  - `idle` otherwise
- **Status line** (pane): `Working` while a run is active, or while subagents or processes are still alive, followed by counts. Connection problems (`connecting…`, `disconnected…`, `not connected`) take precedence.
- **Activity sources** (backend):
  - subagents
  - `command_execution` and `subagent` turn items
  - background tasks of the **active** provider thread (subagent, process, monitor)

  Items from rolled-back runs are left out, and a subagent tracked on its own wins over its transcript row, so nothing is counted twice.

### Composer
| Key | Effect |
|---|---|
| Enter | Send; becomes "queue" while a run is active |
| shift+enter, ctrl+enter, alt+enter, ctrl+j | New line (shift+enter and ctrl+enter need a terminal with modifyOtherKeys) |
| ctrl+a / ctrl+e / ctrl+u / ctrl+w | Line start / end / clear / delete word before the cursor |
| ctrl+← / ctrl+→ (or alt) | Move by a word |
| ctrl+delete (or alt) | Delete the word after the cursor |
| ctrl+backspace (or alt) | Delete the word before the cursor, stopping where ctrl+← would |
| ctrl+c / ctrl+shift+c | Copy the selection: the text selected in the input, else the transcript text the mouse selected. There is one selection at a time: a drag drops the input's, a key drops the transcript's |
| shift+← / shift+→ / shift+home / shift+end | Select; typing or pasting replaces the selection, backspace and delete remove it |
| ctrl+shift+← / ctrl+shift+→ | Select a word at a time |
| ctrl+v | Paste from clipboard: files copied in a file manager attach, each as `[File #n: name]` (an image file as `[Image #n]`); an image (a screenshot) attaches as `[Image #n]`; text is inserted. The input treats a token as one character (the cursor skips it, one delete removes it and drops what it stands for) |
| ↑ / ↓ | Move in the slash popup, else move the cursor a line up or down in a multi-line draft, else (on its first or last line) scroll |
| ↑ on an empty input, a message queued | Take the newest queued message back into the input (`queued-run.cancel` first, so it is never both queued and in the input) to edit and send again, or drop with ctrl+u. Its images and files stay behind, and the note says so. A message taken back never shows as sent |
| Mouse wheel | Scroll the transcript: 3 lines a notch, speeding up on a fast run of notches one way (a trackpad flick, a quick spin) by 3 more every 3 notches, up to 12; a pause of 50 ms or a turn back starts over |
| PgUp / PgDn | Scroll by a page |
| End | Cursor to the end of the input; with the cursor already there, jump to the bottom of the transcript |
| Tab | Complete a slash command |
| ← (empty input) | Activity view |
| Esc | Drop the selection, close the popup, cancel the upload of a message's files, or ask to stop the run |

**Attachments.** Files reach the composer three ways: ctrl+v with files copied in a file manager, pasting a single line that names files, and `/attach <path>`.
- **Clipboard** (`app/clipboard`): when the clipboard offers a file list (`text/uri-list`, else `x-special/gnome-copied-files`, `x-special/KDE-copied-files` or `application/x-kde4-urilist`), its `file://` URIs are percent-decoded and attached in order; `#` comment lines, a desktop's `copy`/`cut` line, web addresses and files on another host (a `file://host/` other than localhost or this machine) are left out. A file list wins over image data, and image data over text: copying an image file in a file manager offers its list (and sometimes its pixels), so the file attaches under its own name; a screenshot offers the image alone; copied text offers text alone. A list that names no local file (a browser's copied image offers its web address) counts as none.
- **Pasted path:** a single pasted line that is an absolute path through a folder (`/a/b`), a `~/` path or a `file://` URI on this machine, quoted (`'…'`, `"…"`) or backslash-escaped as terminals paste a dropped file, attaches the file; several such words separated by spaces attach them all; a path pasted bare keeps its spaces. A slash command (`/help`), a code comment (`// …`) or a relative path stays text, at once. When nothing it names can be attached, the line goes in as text: silently when it names no file, else with the reason (`… is a folder; pasted as text`).
- **What attaches how:** each file is looked at off the UI goroutine (`Files.Stat`): it must be a regular file this user can read; a symlink is followed, a folder, named pipe, socket or device is refused with a note, and nothing is recursed or zipped. An image of an attachable type, by name and content, within T3's 10 MB image limit is read whole and goes as an image (`assets.persistChatAttachments`), as before; one over that limit goes as a file (the note says so). Any other file is checked and only opened again when the message is sent, so a large file is never held in memory, and it goes as it is then. The backend (`chat.FileTaker`) turns down, with a one-line note and nothing attached, an empty file (T3 refuses those), a file over 50 MB, a 101st attachment, and images (image-typed files included) over 80 MB in all. Several files pasted at once attach in order with one note (`3 attached: [File #1] report.pdf, 2.3 MB; …; left out: …`); one says `[File #1] attached: report.pdf, 2.3 MB`. The token names the file, so the agent reading the message text can tell which file is which.
- **Sending:** `t3chat` uploads each file, one at a time, read from disk as it goes: `attachments.createUploadUrl`, then `POST <server base><relativeUrl>` with `Content-Type` the file's type, `Content-Length` its size and the bearer token, over the connection's own transport (the SSH tunnel's socket for `ssh://`) and below the path the server is served under. The server answers 204, stores the file as a pending upload and checks the byte count. Then the images are stored and `message.dispatch` carries `{type: file, id, name, mimeType, sizeBytes}` for each file after the images; T3 copies each pending upload into the thread's own store (`claimPendingAttachments`). A message carrying files has 30 s plus its files at 64 KB/s to go; a transfer that sends nothing for a minute is ended. The message's card reads `Uploading report.pdf: 12.5 MB of 48.0 MB · esc cancels`; esc, switching threads or closing the pane stops the upload and puts the message back in the input. Once the files are up the message goes out on a deadline of its own: cancelling stops an upload, never half a send. The uploads of a message that was not sent are deleted with `attachments.delete`, and so are those of one that was, which the server has copied; T3 sweeps a forgotten pending upload after a day.
- **The agent** gets each file as a path on the server's disk: every provider adapter appends `[Attached file "report.pdf" is saved at: <T3 home>/userdata/attachments/<id>.pdf]` to the message, with the path of a text extraction for documents T3 can read; Claude may also get a PDF inline. An image goes to the agent as an image, as before.
- **Transcript:** a user's message lists what it carries under its text, one row each: `📎 report.pdf · 2.3 MB`, an image's and any other kind's with its kind (`· image`), including messages sent from T3's own client and attachment kinds this client does not know. Names are sanitized like all server text.

When a send fails, the text and its images and files go back into the input; a file gone since it was attached fails the send that way. A command or message that is turned down (bad argument, nothing to approve, not connected, another request still being sent) stays in the input, with the reason underneath. Commands that send nothing, such as `/help` or `/work`, work while a request is in flight. Switching threads keeps the draft and its attachments.

**Queued messages:** the server queues a message that arrives during a run (`queue_after_active`), and it becomes the next turn. The pane names each message it sends with a UUID, which T3 takes as the message id. Its local cards and the server's queue are merged **by message ID only**, never by text; a local card goes once the backend reports the message delivered (the transcript shows it, or its run ended). A card is labeled "Sending…", "Queued" or "Sent". Up to 8 rows of cards are shown.

### Slash commands
| Command | Effect |
|---|---|
| `/help` | List commands |
| `/stop` | Stop the run (asks for confirmation) |
| `/accept` (`/yes`, `/y`), `/decline` (`/no`, `/n`), `/cancel` | Answer the newest pending approval |
| ↑/↓, Enter on an empty input | Choose **Accept** or **Decline** in the pending approval's picker and confirm it (the picker starts on Accept) |
| `/answer <n \| 1,3 \| text>`, `/1`…`/9` | Answer a question. Answers to multi-question prompts are collected until all questions are answered. If sending them fails, only the last answer has to be given again. |
| `/dismiss` | Dismiss a user-input request |
| `/model <model>` *(T3)* | Switch model, by slug, `instance:slug`, or a unique part of the name |
| `/effort [level]` *(T3)* | Show or set the reasoning effort, if the model offers one |
| `/plan`, `/build` (`/default`) *(T3)* | Interaction mode |
| `/mode approval-required\|auto-accept-edits\|auto\|full-access` *(T3)* | Runtime mode |
| `/clear` *(T3)* | Start a new thread in the same project, with this thread's model, runtime and interaction modes; it takes the screen and the chat |
| `/side` (`/btw`) `<question>` *(T3)* | Fork a side chat and ask it; once the fork went through it opens in a pane of its own, right of the other panes, and stays on its thread when the review moves to another (it is listed with the threads too) |
| `/close` | Close a side chat's pane (the `×` on its title row does too); its thread stays on the server. The chat of the thread under review has neither: it closes with its tab |
| `/attach <path>` | Attach a file, an image as an image; quoted, escaped, `~/`, `file://` and relative paths are taken |
| `/work` | Toggle between expanded and summarized agent work |
| `/activity` | Open the activity view |

`/model` and `/effort` wait their turn while the agent works or messages are queued: the change shows as a queued card (`/effort high`) after the messages queued before it, which keep the selection they were queued with, and the thread takes it once nothing it waits for is queued or running. Every message sent from the chat after it carries the new selection, as T3's own client does, so it runs with it whatever the thread says; ↑ takes the card back like a queued message, and the earlier selection stands. With the agent idle and nothing queued, the change applies at once.

The popup lists the pane's own commands, then T3's, then the provider's own slash commands from the catalog (for the thread's provider). When names clash, the pane's command wins over T3's, and both over the provider's. An unknown `/command` is sent to the agent as text. The popup ranks matches as exact, then prefix, alias, substring, then description; provider commands come last. It shows at most 6 rows. The catalog is fetched once per connection, shared by the threads a pane follows on it; a failed fetch is said under the composer and retried only by a command that needs it.

### Stop guard
- Esc (with no popup open) or `/stop` while the agent works opens **Stop agent?**, with **Keep working** selected. The agent works while a run runs, and also after its run ended while subagents, background commands or monitors it launched are still going.
- ← picks Stop, → picks Keep, Tab toggles, Enter confirms, Esc keeps working.
- **Any other key cancels the dialog. Letters never approve a stop.**
- Stop sends `thread.stop`, as T3's own stop does: the server interrupts the run actually running (never a queued one; queued messages are held), marks it stopped even when the provider does not answer, cancels orphaned background work and stops the subagent threads it delegated to. T3 holds the queue after a stop, so when messages are queued the stop is followed by `queue.resume` and the next queued message starts on its own; if that fails the note says the agent stopped but the queue did not start. It is sent only if the run asked about is still active. The draft is kept.
- If the run finished while the dialog was open, the dialog says nothing will be interrupted.

### Activity view
↑/↓, PgUp/PgDn and Home/End select an entry. Enter expands it. Tab switches between Active and Recent. Esc, ← or → close the view. The selection follows the server ID, so it survives streaming updates.

### Selection and copy
Dragging the left mouse button over the transcript selects text from a frozen snapshot, so streaming doesn't move it. Releasing copies it to the clipboard (Linux: `wl-copy` or `xclip`). The pane keeps the mouse while dragging, even outside its bounds.

### Closing
Closing the chat doesn't stop the agent. The quit dialog says: "Closes the chat. The agent keeps working on the server; it reopens with the next thread you open."

## 6. Remote terminals

A terminal opens where the review on screen lives: the thread on screen, or with none the connected server in its selected project. When that session goes through SSH, **Open Terminal** runs:

```
ssh -t -o ClearAllForwardings=yes -o ControlMaster=no -S <dir>/ssh.ctl -- <host> 'cd '<thread cwd>' 2>/dev/null; exec "${SHELL:-/bin/sh}" -l'
```

- **One connection.** `-S` makes the terminal a session on the tunnel's own SSH connection: no second login, no new host-key or password prompt. If the control socket is gone, ssh opens a connection of its own.
- **No duplicate forwards.** The tunnel's ssh already holds the host's `LocalForward`/`RemoteForward`/`DynamicForward` entries from `~/.ssh/config`. `ClearAllForwardings=yes` stops the terminal from binding the same ports again, which would print `Address already in use`, and would fail the login under `ExitOnForwardFailure=yes`.
- **Lifetime.** The terminal lives on the tunnel's connection, so it ends when that T3 session closes: on quit, on a reconnect, and when switching to another server closes the old session.

The directory is single-quote escaped. A `--terminal-command` replaces the `exec …` part and is read by the remote login shell. BatchMode is left off so ssh can prompt inside the pane. Without SSH the pane runs `--terminal-command` locally (through `/bin/sh`), in the thread cwd or the project's workspace root when the server runs on this machine, else in aidiff's own directory.

## 7. Timeouts

| Operation | Timeout |
|---|---|
| Connect (including SSH login and tunnel) | 45 s |
| SSH tunnel listening | 15 s |
| Diff and shell calls | 60 s |
| Palette calls, chat sends | 30 s |
| Chat send carrying files | 30 s plus the files at 64 KB/s; an upload that sends nothing for 1 min ends |
| Ping interval | 25 s |
