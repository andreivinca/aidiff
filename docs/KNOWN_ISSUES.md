# Known issues and gaps

These are limitations found in aidiff's own features at `v0.0.13`.

## Branding

The rename from revdiff is not finished. These places still use the revdiff name:

| Where | Current value |
|---|---|
| Go module | `github.com/umputun/revdiff` |
| Main package and `make build` output | `app/revdiff`, `.bin/revdiff` |
| `.goreleaser.yml`, `flake.nix`, `completions/` | binary `revdiff` |
| Config directory | `~/.config/revdiff/` (config, keybindings, themes, history, T3 tokens) |
| Environment variables | `REVDIFF_*`, including the aidiff-only ones (`REVDIFF_TERMINAL`, `REVDIFF_T3_TOKEN`, …) |
| Error text | "revdiff requires an interactive terminal" |
| Agent plugins, `site/`, `CONTRIBUTING.md`, `CHANGELOG.md` | upstream revdiff throughout |

Renaming the config directory and the env vars would break existing setups, so it needs a fallback or migration plan.

## Features

| Area | Issue |
|---|---|
| Terminal pane | Scrollback (5000 lines) is kept, but there is no way to scroll it. Wheel events go to the program. |
| Terminal pane | On macOS, closing a pane signals only the process group, so background jobs in other groups may survive. |
| T3 | Only the live connection reconnects by itself. A connection kept in the background that drops stays down until it is selected and used again. A link that dies silently (no TCP reset, as after a long sleep) is noticed only when the connection next fails. |
| T3 | Tied to orchestration protocol 2. Any T3 server protocol change needs a client update. |
| T3 | New threads always start in `full-access` runtime mode. |
| T3 | Remote `http://` servers are accepted, which sends the token in cleartext. |
| Clipboard | Linux only (`wl-clipboard` / `xclip`). No macOS `pbcopy`, and no OSC 52 for SSH sessions. |
| Live refresh | Not available for Mercurial or jj repositories, stdin, compare, or `--only` outside a repository. |
| Split view | Shift+enter needs a terminal that honors xterm's modifyOtherKeys (aidiff turns on level 1 at startup); others send it as a plain enter, and alt+enter or ctrl+j still insert a line break. The split reads the reports through Bubble Tea's unexported `unknownCSISequenceMsg` (`rawseq.go`); `TestRawSequence_BubbleTeaType` fails if an upgrade changes it. |
| Platforms | No Windows build. |

## Process

| Item | State |
|---|---|
| Release pipeline | The upstream workflow was removed and nothing replaces it in the repository. |
| CHANGELOG | Has no aidiff entries. |
