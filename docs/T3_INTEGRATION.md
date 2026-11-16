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
| `--t3-source uncommitted` | Review the thread's working tree instead of its branch range. |
| `aidiff --t3-threads` | Print `id  idle\|running  project  branch  title` per thread and exit. |
| ctrl+p → **T3: Connect to server…** (any mode) | Lists `local` (if a runtime file exists), servers with a saved login, and `~/.ssh/config` hosts. Text typed without `://` is taken as `ssh://<text>`. |

In T3 mode the connection is made **before** the full-screen UI starts, so SSH and auth errors print as plain text. The chat pane opens on its own when a thread is selected, unless `--no-chat` is given. The status bar shows the machine name, the T3 project and `T3`, and the info popup gets a `**T3 Code**: <label>` header with the cwd and thread id.

## 2. Connecting

### Server discovery
- **local:** reads `$T3CODE_HOME/userdata/server-runtime.json` (default `~/.t3`), which holds `{pid, host, port, origin}`, and checks that the PID is alive.
- **ssh://host:**
  1. The host is validated against an allowlist: letters, digits and `. _ @ : -`, not starting with `-`.
  2. One `ssh -- host` login reads the remote runtime file and checks the PID on the remote side (exit 3: no state, exit 4: not running).
  3. The client creates a private (0700) temp directory and starts `ssh -N -L <dir>/t3.sock:<remote host>:<remote port>` with `ExitOnForwardFailure=yes` and `StreamLocalBindUnlink=yes`, in its own process group. That ssh is also a ControlMaster on `<dir>/ssh.ctl`, so remote terminal panes share its connection (see §6). Only this user can create a socket there, so the bearer token cannot reach another local process the way a reused TCP port could.
  4. It waits up to 15 s for the socket to accept connections. If ssh exits first, its stderr is reported.
  5. The tunnel lasts for the whole session; closing the session kills ssh and removes the directory. ssh runs under a small `/bin/sh` watchdog whose stdin is a pipe from aidiff: when aidiff ends without closing the session (a crash, `kill -9`), the kernel closes that pipe and the watchdog kills ssh, so the tunnel and the terminals sharing it never outlive aidiff. The exit itself is bounded: once the screen closes, aidiff has 20 s to finish; past that it saves every goroutine's stack to `$TMPDIR/aidiff-shutdown-<pid>.txt` and exits, which ends the tunnel. A SIGHUP or SIGTERM (the terminal window closing, `kill`) quits without the "Quit aidiff?" dialog, which nobody could answer, and starts the same deadline; a second signal exits at once.
  Every ssh command runs with `BatchMode=yes`, `ServerAliveInterval=15` and `ServerAliveCountMax=3`, so a dead link is noticed within about 45 s.
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
| `review.getDiffPreview` | File list (`metadataOnly`) and per-file diffs. `sourceKind` is `branch-range` or `working-tree`. |
| `orchestration.launchThread` | New thread, with the caller's `runtimeMode` (the palette sends `full-access`), `interactionMode: default` and workspace root. |
| `orchestration.dispatchCommand` | Every thread command (below), each with a UUIDv4 `commandId` |
| `server.getConfig` | Provider catalog: models, capabilities, slash commands, default model |
| `assets.persistChatAttachments` | Image uploads |
| `filesystem.browse`, `projects.mutate` | Browse server folders and create projects |

Commands sent through `dispatchCommand`:
- `message.dispatch`, with `dispatchMode: queue_after_active`, plus `titleSeed` and `freshContext` when set
- `runtime-request.respond`
- `thread.user-input.dismiss`
- `thread.stop` (the stop dialog), and `run.interrupt` for a server without it
- `queue.resume` (`/resume`)
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
**There is no automatic reconnect.** If the connection drops, the chat header shows `disconnected: <error>`. ctrl+p → **T3: Reconnect** reconnects and re-resolves the thread, rebuilds the review (annotations kept), restarts the thread watch, and points open chat panes at the new connection.

## 4. Reviewing threads

`t3client.Renderer` implements the review's `Renderer` interface, so everything in [FUNCTIONAL_SPECIFICATION.md](FUNCTIONAL_SPECIFICATION.md) works on T3 diffs. File diffs are fetched one at a time, when opened, once per listing however many views ask at the same time; a fetch that finishes after a reload never lands in the new listing. A preview that lacks the requested source kind is an error, not a fallback to another kind. `WatchThread` sends a refresh when a run starts or finishes, so the diff follows the agent.

Switching thread from the palette:
1. Builds a new review with an empty annotation store.
2. Saves the old review's annotations to history, filed as `<server>:<folder>` (e.g. `ssh://devbox:/srv/wt`) under the folder's name.
3. Restarts the watch, unless `--no-watch` is given.
4. Points open chat panes at the new thread. If none is open and auto-chat is on, a chat pane opens.

**T3: New thread…** gets the default model, launches the thread, and waits for it to appear, polling up to 5 times, 300 ms apart. **T3: Projects…** → **New project…** browses the server filesystem (`..`, typed paths, "Use this folder") and creates a project named after the folder.

**T3: Send annotations to agent** sends the formatted annotations as a message that starts with `Review annotations from aidiff:`.

## 5. Chat pane

The pane (`app/chat`) owns the layout, the composer, the popup, the stop dialog, the activity view and the prompts; the T3 backend (`app/t3chat`) owns what they show of a thread and the commands marked *T3* below.

### Layout
```
 Thread title · Working                              ↑12 · end jumps down
 ───────────────────────────────────────────────────────────────────────
 you: please add tests
 assistant: …
 + 3 commands, 1 edit · /work shows them
 ┌ Queued ───────────────────────────┐
 │ also update the docs              │
 └───────────────────────────────────┘
 Working · 2 subagents · 1 process
 > _                                         enter queue · / commands
```

### State and status
- **Thread state** (backend):
  - `waiting for you` when there is a pending approval or question, or the latest run is waiting
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
| shift+← / shift+→ / shift+home / shift+end | Select; typing or pasting replaces the selection, backspace and delete remove it |
| ctrl+shift+← / ctrl+shift+→ | Select a word at a time |
| ctrl+v | Paste from clipboard: an image attaches as `[Image #n]`, which the input treats as one character (the cursor skips it, one delete removes it and drops the image), text is inserted |
| ↑ / ↓ | Move in the slash popup, else move the cursor a line up or down in a multi-line draft, else (on its first or last line) scroll |
| PgUp / PgDn | Scroll by a page |
| End | Cursor to the end of the input; with the cursor already there, jump to the bottom of the transcript |
| Tab | Complete a slash command |
| ← (empty input) | Activity view |
| Esc | Drop the selection, close the popup, or ask to stop the run |

Pasting a single line that is a path to an image file attaches that file. The backend turns down an image over T3's 10 MB limit, a file before it is read. When a send fails, the text and images go back into the input. A command or message that is turned down (bad argument, nothing to approve, not connected, another request still being sent) stays in the input, with the reason underneath. Commands that send nothing, such as `/help` or `/work`, work while a request is in flight. Switching threads keeps the draft and its images.

**Queued messages:** the server queues a message that arrives during a run (`queue_after_active`), and it becomes the next turn. The pane names each message it sends with a UUID, which T3 takes as the message id. Its local cards and the server's queue are merged **by message ID only**, never by text; a local card goes once the backend reports the message delivered (the transcript shows it, or its run ended). A card is labeled "Sending…", "Queued" or "Sent". Up to 8 rows of cards are shown.

### Slash commands
| Command | Effect |
|---|---|
| `/help` | List commands |
| `/stop` | Stop the run (asks for confirmation) |
| `/accept` (`/yes`, `/y`), `/decline` (`/no`, `/n`), `/cancel` | Answer the newest pending approval |
| `/answer <n \| 1,3 \| text>`, `/1`…`/9` | Answer a question. Answers to multi-question prompts are collected until all questions are answered. If sending them fails, only the last answer has to be given again. |
| `/dismiss` | Dismiss a user-input request |
| `/model <model>` *(T3)* | Switch model, by slug, `instance:slug`, or a unique part of the name |
| `/effort [level]` *(T3)* | Show or set the reasoning effort, if the model offers one |
| `/plan`, `/build` (`/default`) *(T3)* | Interaction mode |
| `/mode approval-required\|auto-accept-edits\|auto\|full-access` *(T3)* | Runtime mode |
| `/resume` *(T3)* | Send the queued messages a stop held back |
| `/clear` *(T3)* | Start a new thread in the same project, with this thread's model, runtime and interaction modes; it takes the screen and the chat |
| `/side` (`/btw`) `<question>` *(T3)* | Fork a side chat and ask it |
| `/attach <image>` | Attach an image file |
| `/work` | Toggle between expanded and summarized agent work |
| `/activity` | Open the activity view |

The popup lists the pane's own commands, then T3's, then the provider's own slash commands from the catalog (for the thread's provider). When names clash, the pane's command wins over T3's, and both over the provider's. An unknown `/command` is sent to the agent as text. The popup ranks matches as exact, then prefix, alias, substring, then description; provider commands come last. It shows at most 6 rows. The catalog is fetched once per connection, shared by the threads a pane follows on it; a failed fetch is said under the composer and retried only by a command that needs it.

### Stop guard
- Esc (with no popup open) or `/stop` while the agent works opens **Stop agent?**, with **Keep working** selected. The agent works while a run runs, and also after its run ended while subagents, background commands or monitors it launched are still going.
- ← picks Stop, → picks Keep, Tab toggles, Enter confirms, Esc keeps working.
- **Any other key cancels the dialog. Letters never approve a stop.**
- Stop sends `thread.stop`, as T3's own stop does: the server interrupts the run actually running (never a queued one; queued messages are held), marks it stopped even when the provider does not answer, cancels orphaned background work and stops the subagent threads it delegated to. T3 holds the queue after a stop: when messages were queued, the input is filled with `/resume` as one block that one delete removes (when it is empty; a draft stays and the note names the command), so Enter sends them. It is sent only if the run asked about is still active. The draft is kept.
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
- **Lifetime.** The terminal lives on the tunnel's connection, so it ends when that T3 session closes: on quit, on **T3: Reconnect**, and when switching to another server closes the old session.

The directory is single-quote escaped. A `--terminal-command` replaces the `exec …` part and is read by the remote login shell. BatchMode is left off so ssh can prompt inside the pane. Without SSH the pane runs `--terminal-command` locally (through `/bin/sh`), in the thread cwd or the project's workspace root when the server runs on this machine, else in aidiff's own directory.

## 7. Timeouts

| Operation | Timeout |
|---|---|
| Connect (including SSH login and tunnel) | 45 s |
| SSH tunnel listening | 15 s |
| Diff and shell calls | 60 s |
| Palette calls, chat sends | 30 s |
| Ping interval | 25 s |
