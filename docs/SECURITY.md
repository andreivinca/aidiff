# Security

This document covers the security properties of aidiff's own features: T3 connection and authentication, SSH, terminal panes, the chat, and live refresh. For the hardening of the review engine itself (VCS argument handling, input size limits), see [revdiff](https://github.com/umputun/revdiff).

## 1. Trust model

- aidiff is a single-user, local tool. It runs with the user's privileges, and anything it starts (shells, ssh, `t3`) gets the same privileges.
- A **T3 server** is trusted with whatever the token scopes allow: reading and operating threads, and reading the filesystem.
- A **remote SSH host** is trusted as much as the user's SSH config trusts it.
- A **repository or folder under review** is treated as untrusted content. aidiff shows it and never executes it. Live refresh only reads it.

## 2. Measures in place

### T3 tokens
- Saved per server in `~/.config/revdiff/t3-tokens.json`. The file is written atomically with mode 0600, in a directory with mode 0700.
- Tokens minted automatically are scoped to `orchestration:read`, `orchestration:operate` and `filesystem:read`, and expire after **30 days**.
- Automatic minting through the `t3` CLI is limited to `local`, loopback URLs and `ssh://` hosts. A remote http(s) server requires explicit pairing (`--t3-pair`, or a `t3 pair` link pasted into the connect picker, which is not repeated in any flash or status and not kept beyond the exchange). The picker's online check sends no token.
- A rejected saved token triggers exactly one re-mint, never a loop.
- The pairing exchange accepts only a `Bearer` token type, and reads at most 1 MiB of response.

### SSH
- The host string is validated before use: a leading `-` and the characters whitespace, `/`, quotes, `` ` ``, `$`, `;`, `&` and `|` are rejected. `--` comes before the host on every ssh command line.
- The tunnel forwards to a Unix socket in a private (0700) temp directory, not a TCP port, so no other local user or process can reach the server through it. It uses `ExitOnForwardFailure=yes`, so a failed forward is noticed. It runs in its own process group and is killed when the session ends, and the directory is removed.
- The tunnel's ControlMaster socket lives in the same private directory, with an explicit `ControlPath`, so it never joins a master from the user's own ssh config. Only remote terminal panes use it (`ssh -S`).
- Discovery and token minting run with `BatchMode=yes`, so ssh never waits on a hidden prompt.
- A host that wants a password, code or host-key confirmation gets one interactive login, run by the system ssh with the screen handed to it: aidiff never sees or stores what is typed. Its ControlMaster socket lives in a per-user directory that is used only when it is a plain 0700 directory owned by this user (else a fresh private temp directory), and the login exits 60 s after its last session.
- In remote terminal panes, the `cd` directory is single-quote escaped.

### Agent control
- Stopping a run requires an explicit confirmation, with "Keep working" selected by default. Typed letters cancel the dialog and never confirm it. Only the run that was asked about is interrupted.
- Chat messages are matched to queue entries by message ID, never by text, so one message can't be mistaken for another.

### Terminal panes
- Closing a pane sends SIGHUP to every process in its session, then SIGKILL after 1 s, so nothing keeps running in the background. On macOS the signal goes to the process group only.
- The quit dialog prevents closing running panes by accident.

### Live refresh
- The watcher never follows symlinks, ignores paths outside the root, and skips git-ignored directories. Folder mode watches only expanded folders.

## 3. Known risks

| Risk | Detail | Mitigation |
|---|---|---|
| Cleartext `http://` T3 servers | Remote `http://` URLs are accepted, so the bearer token and the pairing exchange travel unencrypted. | Use `https://` or `ssh://` for remote servers. |
| Token on the command line | `--t3-token` is visible in `ps`. `REVDIFF_T3_TOKEN` is passed on to every terminal pane's environment. | Prefer pairing or saved tokens. |
| New threads start in `full-access` mode | `LaunchThread` always requests `runtimeMode: full-access`. | Change it with `/mode approval-required` in the chat. *(Consider a safer default.)* |
| Messages labelled `creationSource: "web"` | The T3 server can't tell aidiff's messages apart from its web client's. | — |
| File path paste and copied files | Pasting a single line that names a local file (a path or a `file://` URI), or files copied in a file manager, attaches them: an image is read at once, any other file is read and uploaded to the T3 server, which may be another machine, when the message is sent. The agent can then read it there. | The note names each file and its size, its token in the input names it, and deleting the token drops it before sending; esc cancels an upload. Only regular files are taken (no folders, pipes or devices); a slash command, code comment or relative path stays text. |
| Clipboard | Selected chat text is copied to the local desktop clipboard, also when the session is remote. | — |
| Protocol coupling | T3's RPC is internal and pinned to version 2. A server change could break the client. | A mismatch fails at connect time (HTTP 426) with a clear message. |
| Automatic reconnect | A dropped live connection is redialed the way it was made (1 s, 2 s, 5 s, 10 s, then every 30 s) until it answers or the user connects elsewhere. | Each try is reported on the bottom row; a background (not live) connection is not redialed. |

## 4. Reporting

Report security issues privately to the aidiff maintainer through the GitHub repository (`andreivinca/aidiff`). Don't open a public issue for them.
