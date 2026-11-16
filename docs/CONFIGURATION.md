# Configuration

aidiff accepts all of revdiff's options and adds its own. This page covers the options aidiff adds. For revdiff's options, run `aidiff --help` or `aidiff --dump-config`, or see the [revdiff docs](https://github.com/umputun/revdiff).

Options are resolved in this order: **CLI flag, then environment variable, then config file, then built-in default.**

## Files

aidiff still uses revdiff's config directory.

| Path | Purpose | Override |
|---|---|---|
| `~/.config/revdiff/config` | INI config file; keys are the long flag names | `--config`, `REVDIFF_CONFIG` |
| `~/.config/revdiff/keybindings` | Key remapping | `--keys`, `REVDIFF_KEYS` |
| `~/.config/revdiff/history/` | Auto-saved annotations (T3 reviews go under `t3/`) | `--history-dir` |
| `~/.config/revdiff/t3-tokens.json` | Saved T3 tokens, one per server. Mode 0600 in a 0700 directory. Lives next to the config file. | follows `--config` |
| `$T3CODE_HOME/userdata/server-runtime.json` (default `~/.t3`) | Locates the local T3 server (read only) | `T3CODE_HOME` |
| `~/.ssh/config` | `Host` entries offered in the palette's connect list (read only; wildcard entries are skipped) | — |

## aidiff options

### Terminal, chat, live refresh
| Flag | Env | In config file | Default | Description |
|---|---|---|---|---|
| `--terminal` | `REVDIFF_TERMINAL` | yes | false | open a terminal pane at startup |
| `--terminal-width` | `REVDIFF_TERMINAL_WIDTH` | yes | 45 | width of the right-hand panes, in percent (10–90) |
| `--terminal-command` | `REVDIFF_TERMINAL_COMMAND` | yes | `$SHELL` | shell command line run in a terminal pane, by `/bin/sh` here or by the login shell on the T3 session's SSH host |
| `--no-chat` | `REVDIFF_NO_CHAT` | yes | false | don't open the chat pane automatically when a T3 thread is selected |
| `--no-watch` | `REVDIFF_NO_WATCH` | yes | false | turn off live refresh |

### T3 Code
| Flag | Env | In config file | Default | Description |
|---|---|---|---|---|
| `--t3` | — | no | — | server: `local`, `ssh://host`, or `http(s)://host:port` |
| `--t3-thread` | — | no | most recently updated | thread id or id prefix |
| `--t3-source` | `REVDIFF_T3_SOURCE` | yes | `uncommitted` | `uncommitted` (the thread's working tree) or `branch` (its branch range against the base, unpushed commits included) |
| `--t3-pair` | — | no | — | pairing link or code from `t3 pair` |
| `--t3-token` | `REVDIFF_T3_TOKEN` | no | — | bearer token; skips login |
| `--t3-threads` | — | no | — | list the server's threads and exit |

**Rules:**
- The T3 flags can't be combined with refs, `--staged`, `--only`, `--all-files`, `--stdin`, `--compare-*`, `--include`, `--exclude` or `--annotations`. T3 decides what is reviewed.
- Folder browsing has no flag of its own. It starts when aidiff runs outside a repository without `--only`. `--include` and `--exclude` still filter the tree.

### Example config
```ini
terminal = true
terminal-width = 40
terminal-command = zsh -l
t3-source = branch
```

## Fixed keys

aidiff handles these keys itself, before revdiff's keymap, so the keybindings file can't change them:

| Key | Action |
|---|---|
| ctrl+p | command palette |
| alt+[ / alt+] | focus the panel to the left / right, stopping at either end |
| ctrl+q | quit (asks first when panes are open) |

The review's own keys can be remapped in `~/.config/revdiff/keybindings` (`map <key> <action>`, `unmap <key>`). Run `aidiff --dump-keys` to see the current bindings.

## Environment variables read by aidiff features

| Variable | Used for |
|---|---|
| `SHELL` | Default terminal-pane command |
| `T3CODE_HOME` | Local T3 runtime file location |
| `WAYLAND_DISPLAY`, `DISPLAY` | Choosing a clipboard tool (`wl-clipboard` or `xclip`) |
| `REVDIFF_T3_TOKEN`, `REVDIFF_T3_SOURCE`, `REVDIFF_TERMINAL*`, `REVDIFF_NO_CHAT`, `REVDIFF_NO_WATCH` | See the tables above |
