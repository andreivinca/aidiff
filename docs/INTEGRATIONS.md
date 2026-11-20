# Integrations

This document covers how annotations leave aidiff and how other tools drive it. The output format, exit codes and agent plugins all come from [revdiff](https://github.com/umputun/revdiff), and aidiff uses them as they are. This page describes how aidiff uses them and what aidiff adds.

## 1. Ways to deliver annotations

| Path | Trigger | Source |
|---|---|---|
| stdout or `-o FILE` on quit | `q` | revdiff |
| `-o` and/or `--post-flush-command` while the review stays open | `O` | revdiff |
| History file (safety net, also written on SIGHUP/SIGTERM) | on exit | revdiff |
| **Straight to the T3 thread as a message** | ctrl+p → **T3: Send annotations to agent** | **aidiff** |
| **The clipboard, to paste anywhere** (e.g. an agent in a terminal pane) | ctrl+p → **Copy annotations** | **aidiff** |
| **Any command, in a terminal pane** | e.g. `cat /tmp/review.md` after an `O` flush to `-o /tmp/review.md` | **aidiff** |

The T3 path sends revdiff's markdown output, prefixed with `Review annotations from aidiff:`, as a normal user message. If the agent is running, the message is queued and becomes the next turn.

## 2. Annotation output format (revdiff)

```
## app/main.go (file-level)
Split this file.

## app/main.go:42 (+)
Document this constant.

## app/main.go:100-112 (-)
Why was this removed?
```

The header is `## <path>[:line[-end]] (<file-level|+|-| >)`. `+` marks an added line, `-` a removed line, and a space a context line. `--annotations FILE` reads this format back in to resume a review.

## 3. Exit behavior (revdiff)

| Exit code | Meaning |
|---|---|
| 0 | Normal quit |
| 1 | Error |
| 10 | Annotations exist, when `--exit-code-on-annotations` (or `REVDIFF_EXIT_CODE_ON_ANNOTATIONS=true`) is set |

Other behavior on exit:
- `Q` (discard) writes nothing.
- Annotations are saved to `~/.config/revdiff/history/` whenever there are any. In T3 mode they are saved under `t3/`.
- When the user switches T3 threads, the annotations of the thread being left are saved first.

aidiff adds one step before this: every open pane is closed (its processes get SIGHUP, then SIGKILL) before revdiff writes the output.

## 4. Agent plugins (inherited)

The repository still ships revdiff's agent integrations unchanged. They launch the binary by the name **`revdiff`**:

| Integration | Location |
|---|---|
| Claude Code plugin (diff review skill) | `.claude-plugin/` |
| Claude Code / Codex plan review | `plugins/revdiff-planning/` |
| Codex skills | `plugins/codex/` |
| OpenCode (v1 and v2) | `plugins/opencode/` |
| pi | `plugins/pi/`, root `package.json` |

They open the review in a popup or overlay (tmux, zellij, kitty, wezterm, Ghostty, iTerm2, cmux, herdr, agterm, …) through `launch-revdiff.sh`, and read the result from the output file and exit code 10.

To use them with aidiff, the binary must be on PATH as `revdiff`. The default `make build` output, `.bin/revdiff`, already has that name. None of the plugins know about aidiff features such as the terminal pane or T3. [KNOWN_ISSUES.md](KNOWN_ISSUES.md#branding) has the details.

## 5. T3 Code

T3 is the integration aidiff adds. See [T3_INTEGRATION.md](T3_INTEGRATION.md).
