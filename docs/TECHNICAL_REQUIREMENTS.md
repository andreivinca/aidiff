# Technical requirements

This document lists the platforms, dependencies and non-functional requirements for aidiff's own features. The review engine's requirements come from [revdiff](https://github.com/umputun/revdiff). For example, git, hg or jj must be installed for repository reviews. Requirement IDs (`TR-x`) are for traceability.

## 1. Platforms

| Requirement | Detail |
|---|---|
| **TR-1. OS** | Linux and macOS on amd64 and arm64. Windows is not supported: the terminal pane, SSH tunnel and process cleanup use Unix-only calls (`syscall.Kill`, `Setpgid`, PTYs). |
| **TR-2. Terminal** | An interactive TTY with true color. Mouse support is recommended for drag-resize, selection and focus by click. |
| **TR-3. Toolchain** | Go 1.26, `CGO_ENABLED=0`, dependencies vendored and committed. |
| **TR-4. Linux-only** | Chat clipboard: pasting copied files or an image, and copy-on-select. Pasted paths and `/attach` work on macOS too. It needs `wl-clipboard` (Wayland) or `xclip` (X11). Full process cleanup when a terminal pane closes uses `/proc`; on macOS only the process group is signalled. |

## 2. Runtime dependencies (aidiff features)

| Tool | Needed for | Required? |
|---|---|---|
| `$SHELL` or `--terminal-command` | Terminal panes | For terminals |
| OpenSSH `ssh` | `--t3 ssh://host`, remote terminal panes | For SSH T3 |
| T3 Code server | T3 mode; must speak orchestration protocol **2** | For T3 |
| `t3` CLI (local, or on the SSH host in `PATH`, `~/.local/bin` or `~/.t3/bin`) | Automatic token minting | Optional; `--t3-pair` works without it |
| `wl-paste` / `wl-copy` or `xclip` | Chat clipboard | Optional |
| SSH key or agent auth | The tunnel runs with `BatchMode=yes` and cannot prompt | For SSH T3 |

Go libraries added by aidiff:

| Library | Version | Used for |
|---|---|---|
| `creack/pty` | v1.1.24 | PTY |
| `charmbracelet/x/vt` | v0.1.0 | terminal emulator |
| `charmbracelet/ultraviolet` | — | key and mouse types |
| `coder/websocket` | v1.8.14 | T3 |
| `fsnotify/fsnotify` | v1.10.1 | live refresh |

## 3. Non-functional requirements

### Performance
- **TR-5. No startup stall.** Startup must not wait on terminals that don't answer color queries. A patched termenv (`third_party/termenv`) cuts the wait from 5 s to 150 ms, and `TestStartupSilentTerminal` checks it.
- **TR-6. Terminal latency.** The UI runs at 120 FPS. A terminal pane redraws at most once per 16 ms, but the first change after a quiet spell is shown at once.
- **TR-7. Terminal typing does not redraw the review.** Splitview caches the review's view until the review receives a message.
- **TR-8. Large folders open at once.** Folder browsing reads a directory only when it is expanded, and watches only the root and expanded folders.
- **TR-9. Refresh without polling.** File watching uses OS notifications (inotify, kqueue or FSEvents). A T3 thread refreshes from server events. Nothing polls.

### Reliability
- **TR-10. A refresh never loses work.** Live refresh keeps annotations, keeps the cursor on the same content, and waits while the user is typing or a popup is open.
- **TR-11. Panes don't leak processes.** Closing a pane or quitting sends SIGHUP to its processes, then SIGKILL after 1 s.
- **TR-12. Accidental quit is guarded.** Quitting while panes are open asks first, with Cancel selected by default.
- **TR-13. Accidental agent stop is guarded.** A stop must be confirmed explicitly, typing letters never confirms it, and it interrupts only the run the user was asked about.
- **TR-14. Async results can't go stale.** Each chat subscription has an ID, and events or call results from a replaced subscription are dropped.
- **TR-15. Degrade, don't fail.** If the watcher fails to start, aidiff warns and continues. A missing clipboard tool gives an explanatory error. A lost T3 connection shows `disconnected` in the chat header, and the review stays usable.
- **TR-16. Atomic token storage.** The T3 token file is written to a temp file and renamed into place, with mode 0600 in a 0700 directory.

### Maintainability
- **TR-17. Upstream mergeability.** Fork code lives in `splitview`, `termpane`, `watch`, `t3client`, `chat`, `t3chat`, `t3palette`, `clipboard` and `attachfile`. Hooks into revdiff code are limited to `RefreshMsg`/`CanRefresh`, folder browsing, and the status-bar identity.
- **TR-18. No OS work in the review UI.** PTYs, sockets, fsnotify and the clipboard stay out of `app/ui`.
- **TR-19. Quality gates.** Tests run under the race detector, golangci-lint must pass, and shellcheck runs on the scripts (see [DEVELOPMENT.md](DEVELOPMENT.md)).

### Compatibility
- **TR-20. revdiff compatibility.** The output format, `-o`, exit code 10 and `REVDIFF_*` variables are unchanged, so revdiff's agent plugins and launchers keep working.
- **TR-21. T3 protocol.** aidiff uses Effect RPC over WebSocket at `/ws?orchestrationProtocol=2`. If the server speaks a different version, it rejects the connection with HTTP 426, and aidiff tells the user to update one side.

## 4. Limits and timeouts (aidiff features)

| Item | Value |
|---|---|
| Right-hand pane share | 10–90 %, default 45 % |
| Minimum review width / pane width | 20 / 10 columns |
| Terminal scrollback | 5000 lines (kept but not viewable yet) |
| Terminal redraw | ≤ 1 per 16 ms |
| Pane close grace | 1 s, then SIGKILL |
| Chat image | 10 MiB; png, jpg, gif, webp, bmp (a larger one goes as a file) |
| Chat file | 1 byte to 50 MiB, any type; 100 attachments a message; images and image-typed files 80 MiB together |
| Chat send carrying files | 30 s plus the files at 64 KiB/s; an upload silent for 1 min ends |
| Queued message cards shown | 8 rows |
| Recent threads in the palette | 8 |
| T3 websocket frame | 256 MiB |
| T3 connect, including SSH | 45 s |
| SSH tunnel ready | 15 s |
| T3 diff / shell call | 60 s |
| Palette call / chat send | 30 s |
| T3 keep-alive ping | 25 s |
| Clipboard read / write | 10 s / 5 s |
| Auto-minted T3 token lifetime | 30 days |
