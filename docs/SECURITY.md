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
- Automatic minting through the `t3` CLI is limited to `local`, loopback URLs and `ssh://` hosts. A remote http(s) server requires explicit pairing (`--t3-pair`).
- A rejected saved token triggers exactly one re-mint, never a loop.
- The pairing exchange accepts only a `Bearer` token type, and reads at most 1 MiB of response.

### SSH
- The host string is validated before use: a leading `-` and the characters whitespace, `/`, quotes, `` ` ``, `$`, `;`, `&` and `|` are rejected. `--` comes before the host on every ssh command line.
- The tunnel binds only to `127.0.0.1` and uses `ExitOnForwardFailure=yes`, so a failed forward is noticed. It runs in its own process group and is killed when the session ends.
- Discovery and token minting run with `BatchMode=yes`, so ssh never waits on a hidden prompt.
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
| Image path paste | Pasting a single line that is an image path reads and uploads that local file when the message is sent. | A note `[Image #n] attached` is shown, and the image can be removed before sending. |
| Clipboard | Selected chat text is copied to the local desktop clipboard, also when the session is remote. | — |
| Protocol coupling | T3's RPC is internal and pinned to version 2. A server change could break the client. | A mismatch fails at connect time (HTTP 426) with a clear message. |
| No reconnect | A dropped connection stays down until **T3: Reconnect**. | The status is shown in the chat header. |

## 4. Reporting

Report security issues privately to the aidiff maintainer through the GitHub repository (`andreivinca/aidiff`). Don't open a public issue for them.
