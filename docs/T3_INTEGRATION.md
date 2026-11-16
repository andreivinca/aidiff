# T3 Code integration

[T3 Code](https://github.com/pingdotgg/t3code) is a server that runs coding agents in threads, one thread per project and conversation. aidiff can act as a T3 client. It shows a thread's changes in the review UI, chats with the agent in a pane, and opens terminals where the agent runs.

The code lives in `app/t3client` (protocol and connection), `app/t3chat` (chat pane), and `app/revdiff/t3*.go` (wiring, palette and terminal start-up).

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

In T3 mode the connection is made **before** the full-screen UI starts, so SSH and auth errors print as plain text. The chat pane opens on its own when a thread is selected, unless `--no-chat` is given. The status bar shows the machine name and `T3`, and the info popup gets a `**T3 Code**: <label>` header with the cwd and thread id.

## 2. Connecting

### Server discovery
- **local:** reads `$T3CODE_HOME/userdata/server-runtime.json` (default `~/.t3`), which holds `{pid, host, port, origin}`, and checks that the PID is alive.
- **ssh://host:**
  1. The host is validated: it must not start with `-`, and must not contain whitespace, `/`, quotes, `` ` ``, `$`, `;`, `&` or `|`.
  2. `ssh -o BatchMode=yes -- host` reads the remote runtime file and checks the PID on the remote side.
  3. The client picks a free local port and starts `ssh -N -L 127.0.0.1:<port>:<remote host>:<remote port>` with `ExitOnForwardFailure=yes`, in its own process group.
  4. It waits up to 15 s for the port to start listening. If ssh exits first, its stderr is reported.
  5. The tunnel lasts for the whole session.
- **http(s)://:** used as given.

### Authentication
The client tries tokens in this order:
1. `--t3-token` / `REVDIFF_T3_TOKEN`.
2. A `--t3-pair` exchange: `POST /oauth/token`, an OAuth token exchange with `subject_token_type=urn:t3:params:oauth:token-type:environment-bootstrap`, `client_label=aidiff` and `client_device_type=bot`. The token is saved afterwards.
3. A token saved earlier in `t3-tokens.json`.
4. A freshly minted token. This is allowed only for local, loopback and SSH servers. aidiff runs `t3 auth session issue --label aidiff --ttl 30d --token-only --scope orchestration:read --scope orchestration:operate --scope filesystem:read`, locally or over SSH (adding `~/.local/bin` and `~/.t3/bin` to PATH).

If a saved token is rejected, the client mints one new token and retries once. If it can't mint, the error tells the user to run `t3 pair` and pass `--t3-pair`.

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
| Keep-alive | `{"_tag":"Ping"}` every 25 s |
| Errors | 401/403 → unauthorized. 426 → protocol mismatch ("update one of them"). |
| Max frame | 256 MiB (a diff preview arrives in a single frame) |

### Methods used
| Method | Purpose |
|---|---|
| `orchestration.subscribeShell` | Projects and threads snapshot (`FetchShell`), and run start/finish notifications (`WatchThread`) |
| `orchestration.subscribeThread` | The full transcript stream for the chat pane |
| `review.getDiffPreview` | File list (`metadataOnly`) and per-file diffs. `sourceKind` is `branch-range` or `working-tree`. |
| `orchestration.launchThread` | New thread. Always sent with `runtimeMode: full-access`, `interactionMode: default` and workspace root. |
| `orchestration.dispatchCommand` | Every thread command (below), each with a UUIDv4 `commandId` |
| `server.getConfig` | Provider catalog: models, capabilities, slash commands, default model |
| `assets.persistChatAttachments` | Image uploads |
| `filesystem.browse`, `projects.mutate` | Browse server folders and create projects |

Commands sent through `dispatchCommand`:
- `message.dispatch`, with `dispatchMode: queue_after_active`, plus `titleSeed` and `freshContext` when set
- `runtime-request.respond`
- `thread.user-input.dismiss`
- `run.interrupt`
- `thread.interaction-mode.set`
- `thread.fork` (side chat)
- `thread.model-selection.set`
- `thread.runtime-mode.set`

### Stream model
A thread stream starts with a `snapshot` (messages, runs, runtime requests, turn items, subagents, provider threads), followed by `event`s:
- `message.updated`, `turn-item.updated`, `thread.*`
- `run.created`, `run.updated`
- `runtime-request.updated`, `subagent.updated`, `provider-thread.updated`

The server re-sends whole entities, so streaming text arrives as the same item with more text. A run counts as active while it is preparing, queued, starting, running or waiting.

### Reconnection
**There is no automatic reconnect.** If the connection drops, the chat header shows `disconnected: <error>`. ctrl+p → **T3: Reconnect** reconnects and re-resolves the thread, rebuilds the review (annotations kept), restarts the thread watch, and points open chat panes at the new connection.

## 4. Reviewing threads

`t3client.Renderer` implements the review's `Renderer` interface, so everything in [FUNCTIONAL_SPECIFICATION.md](FUNCTIONAL_SPECIFICATION.md) works on T3 diffs. File diffs are fetched one at a time, when opened. `WatchThread` sends a refresh when a run starts or finishes, so the diff follows the agent.

Switching thread from the palette:
1. Builds a new review with an empty annotation store.
2. Saves the old review's annotations to history (under `t3`).
3. Restarts the watch.
4. Points open chat panes at the new thread. If none is open and auto-chat is on, a chat pane opens.

**T3: New thread…** gets the default model, launches the thread, and waits for it to appear, polling up to 5 times, 300 ms apart. **T3: Projects…** → **New project…** browses the server filesystem (`..`, typed paths, "Use this folder") and creates a project named after the folder.

**T3: Send annotations to agent** sends the formatted annotations as a message that starts with `Review annotations from aidiff:`.

## 5. Chat pane

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
- **Thread state:**
  - `waiting for you` when there is a pending approval or question, or the latest run is waiting
  - `running` when the latest run is active
  - `idle` otherwise
- **Status line:** `Working` while a run is active, or while subagents or processes are still alive, followed by counts. Connection problems (`connecting…`, `disconnected…`, `not connected`) take precedence.
- **Activity sources:**
  - subagents
  - `command_execution` and `subagent` turn items
  - background tasks of the **active** provider thread (subagent, process, monitor)

  Items from rolled-back runs are left out, and a subagent tracked on its own wins over its transcript row, so nothing is counted twice.

### Composer
| Key | Effect |
|---|---|
| Enter | Send; becomes "queue" while a run is active |
| shift+enter, alt+enter, ctrl+j | New line |
| ctrl+a / ctrl+e / ctrl+u / ctrl+w | Line start / end / clear / delete word |
| ctrl+v | Paste from clipboard: an image attaches as `[Image #n]`, text is inserted |
| ↑ / ↓ | Move in the slash popup, otherwise scroll |
| PgUp / PgDn | Scroll by a page |
| Tab | Complete a slash command |
| ← (empty input) | Activity view |
| Esc | Close the popup, or ask to stop the run |

Pasting a single line that is a path to an image file attaches that file. When a send fails, the text and images go back into the input.

**Queued messages:** the server queues a message that arrives during a run (`queue_after_active`), and it becomes the next turn. Local cards and the server's queue are merged **by message ID only**, never by text. A card is labeled "Sending…", "Queued" or "Sent". Up to 8 rows of cards are shown.

### Slash commands
| Command | Effect |
|---|---|
| `/help` | List commands |
| `/stop` | Stop the run (asks for confirmation) |
| `/accept` (`/yes`, `/y`), `/decline` (`/no`, `/n`), `/cancel` | Answer the newest pending approval |
| `/answer <n \| 1,3 \| text>`, `/1`…`/9` | Answer a question. Answers to multi-question prompts are collected until all questions are answered. |
| `/dismiss` | Dismiss a user-input request |
| `/model <model>` | Switch model, by slug, `instance:slug`, or a unique part of the name |
| `/effort [level]` | Show or set the reasoning effort, if the model offers one |
| `/plan`, `/build` (`/default`) | Interaction mode |
| `/mode approval-required\|auto-accept-edits\|auto\|full-access` | Runtime mode |
| `/clear` | The next message starts a fresh provider context; the transcript stays |
| `/side` (`/btw`) `<question>` | Fork a side chat and ask it |
| `/attach <image>` | Attach an image file |
| `/work` | Toggle between expanded and summarized agent work |
| `/activity` | Open the activity view |

The provider's own slash commands come from the catalog and appear in the popup too. When names clash, aidiff's command wins. An unknown `/command` is sent to the agent as text. The popup ranks matches as exact, then prefix, alias, substring, then description; provider commands come last. It shows at most 6 rows.

### Stop guard
- Esc (with no popup open) or `/stop` during an active run opens **Stop agent?**, with **Keep working** selected.
- ← picks Stop, → picks Keep, Tab toggles, Enter confirms, Esc keeps working.
- **Any other key cancels the dialog. Letters never approve a stop.**
- Only the run that was asked about is interrupted, and only if it is still active. The draft is kept.
- If the run finished while the dialog was open, the dialog says nothing will be interrupted.

### Activity view
↑/↓, PgUp/PgDn and Home/End select an entry. Enter expands it. Tab switches between Active and Recent. Esc, ← or → close the view. The selection follows the server ID, so it survives streaming updates.

### Selection and copy
Dragging the left mouse button over the transcript selects text from a frozen snapshot, so streaming doesn't move it. Releasing copies it to the clipboard (Linux: `wl-copy` or `xclip`). The pane keeps the mouse while dragging, even outside its bounds.

### Closing
Closing the chat doesn't stop the agent. The quit dialog says: "Closes the chat. The agent keeps working on the server; reopen it from the palette."

## 6. Remote terminals

When the live T3 session goes through SSH, **Open Terminal** runs:

```
ssh -t -- <host> 'cd '<thread cwd>' 2>/dev/null; exec "${SHELL:-/bin/sh}" -l'
```

The directory is single-quote escaped. BatchMode is left off so ssh can prompt inside the pane. Without SSH the pane runs `--terminal-command` locally, in the thread cwd or the project's workspace root.

## 7. Timeouts

| Operation | Timeout |
|---|---|
| Connect (including SSH login and tunnel) | 45 s |
| SSH tunnel listening | 15 s |
| Diff and shell calls | 60 s |
| Palette calls, chat sends | 30 s |
| Ping interval | 25 s |
