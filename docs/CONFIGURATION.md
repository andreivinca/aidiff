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
| `--terminal` | `REVDIFF_TERMINAL` | yes | false | open a terminal pane at startup, with a T3 thread too |
| `--terminal-width` | `REVDIFF_TERMINAL_WIDTH` | yes | 45 | width of the right-hand panes, in percent (10–90) |
| `--terminal-command` | `REVDIFF_TERMINAL_COMMAND` | yes | `$SHELL` | shell command line run in a terminal pane, by `/bin/sh` here or by the login shell on the T3 session's SSH host |
| `--no-chat` | `REVDIFF_NO_CHAT` | yes | false | don't open the chat pane automatically when a T3 thread is selected |
| `--no-terminal` | `REVDIFF_NO_TERMINAL` | yes | false | don't open a terminal pane automatically with a local folder (the run's own, or one opened from the history) |
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
| ctrl+p | command palette (`>` commands; delete it to search files by name) |
| ctrl+b | show / hide the side panel: the T3 projects panel while a T3 server is selected, the history panel while `local` is |
| ctrl+1 … ctrl+9, alt+1 … alt+9 | switch to T3 project 1–9 (while connected) |
| alt+← / alt+→ | focus the panel to the left / right, stopping at either end; the review's file tree and diff count as two panels. Past the leftmost panel alt+← shows the side panel (T3 projects, or the local history), then the connections panel |
| ctrl+q | quit (asks first when panes are open) |

`e` and F12 open the editor (`$EDITOR`) in the project's root folder, with the file on screen (at the cursor's line when the editor takes one), or with no file when none is on screen. They work with the file tree or the diff focused, in local reviews and in T3 reviews: with a server on this machine the editor runs here; with one reached over SSH it runs on that host, over the session's SSH connection (as terminal panes do), in the thread's folder, and is the host's `$EDITOR` (then `$VISUAL`, then `vi`) as its login shell sets it. When your own `$EDITOR` is `fresh`, it runs here instead and opens the host's file itself, as `ssh://<host>/<folder>/<file>:<line>`, over an SSH connection of its own. A T3 server reached without SSH has no editor. After the editor closes, a T3 review reloads the file. A file with line annotations in a working-tree review is not opened, as in revdiff.

The review's own keys can be remapped in `~/.config/revdiff/keybindings` (`map <key> <action>`, `unmap <key>`). Run `aidiff --dump-keys` to see the current bindings.

## Environment variables read by aidiff features

| Variable | Used for |
|---|---|
| `SHELL` | Default terminal-pane command |
| `EDITOR`, then `VISUAL` | The editor `e` / F12 open (revdiff's `open_file_in_editor`); `vi` when neither is set. Set it in the shell profile, e.g. `export EDITOR=fresh`. For a T3 review over SSH, the host's own variable counts |
| `T3CODE_HOME` | Local T3 runtime file location |
| `WAYLAND_DISPLAY`, `DISPLAY` | Choosing a clipboard tool (`wl-clipboard` or `xclip`) |
| `REVDIFF_T3_TOKEN`, `REVDIFF_T3_SOURCE`, `REVDIFF_TERMINAL*`, `REVDIFF_NO_CHAT`, `REVDIFF_NO_WATCH` | See the tables above |
