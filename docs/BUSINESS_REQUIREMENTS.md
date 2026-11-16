# Business requirements

This document gives the problem aidiff solves, who it is for, and the business-level requirements the product meets. It was written from what the code does at `v0.0.11`. A requirement here describes behavior that exists, unless it is marked *(gap)*.

## 1. Problem

AI coding agents now write a large share of code changes. The developer's job shifts from writing code to **reviewing what the agent changed and telling it what to fix**. The usual tools are bad at this:

- **Web review tools** (GitHub, GitLab) need a pushed branch and a PR. That is too slow for a loop that repeats every few minutes on uncommitted changes.
- **IDE diff views** show changes, but there is no way to turn line comments into structured input for an agent.
- **Chat** loses the link between a comment and the exact line, file and side of the diff it is about.
- **Switching windows** between a diff tool, a terminal and the agent's chat breaks concentration, and the review goes stale as the agent keeps editing.

revdiff, the upstream project, already solved the first part: a fast terminal diff reviewer that writes annotations in a format agents can read. aidiff builds on it so that the **review, the shell, and the conversation with the agent all happen in one terminal window**, and the review stays current while the agent works.

## 2. Product vision

> Watch the agent's changes appear, annotate them in place, and send the annotations to the agent, all from a single terminal screen.

## 3. Target users

| Persona | Needs |
|---|---|
| **Agent-assisted developer** (primary) | Reviews an agent's uncommitted or branch changes many times an hour. Wants line comments delivered to the agent with no copy-paste. |
| **T3 Code user** | Runs agents on a T3 Code server, locally or on a remote dev box over SSH. Wants to see thread diffs, chat with the agent, and approve or stop runs without the T3 web UI. |
| **Terminal-centric engineer** | Works in tmux, zellij, kitty, wezterm or similar. Wants a keyboard-driven reviewer with vim-style motions and an embedded shell. |
| **Document reviewer** | Reviews plans, specs and markdown, sometimes outside any repository. Wants to annotate a file or a folder of files. |
| **Agent-tool integrator** | Builds a workflow (Claude Code, Codex, OpenCode, pi) that launches a review and reads the result through a stable output format and exit code. |

## 4. Goals and non-goals

### Goals
- **G1.** Make reviewing an agent's change faster than reading it in an IDE or a web PR.
- **G2.** Turn review comments into structured, line-anchored feedback any agent can consume.
- **G3.** Keep the review current while files change, without losing the user's place or comments.
- **G4.** Put the shell and the agent conversation beside the review, so the user never leaves the window.
- **G5.** Work with git, Mercurial and Jujutsu repositories, with plain folders, and with T3 Code threads, including remote ones.
- **G6.** Stay compatible with revdiff's output format, exit codes and agent plugins, so existing workflows keep working.

### Non-goals
- Hosting reviews, or sharing them between people. aidiff is single-user and local.
- Replacing a full editor. It opens `$EDITOR` for editing, and does not edit source itself.
- Merging, committing, staging or any other VCS write operation.
- A graphical or web interface.
- Windows support (see [TECHNICAL_REQUIREMENTS.md](TECHNICAL_REQUIREMENTS.md#1-platforms)).

## 5. Scope

| In scope | Out of scope |
|---|---|
| Folder browsing without a repository | Multi-user collaboration |
| Live refresh on file changes | VCS write operations |
| Diff/annotation engine — used as-is from revdiff | Changing how revdiff reviews work |
| Embedded terminal panes (local, and remote over SSH for T3 sessions) | Terminal scrollback viewer *(gap)* |
| T3 Code: connect, browse threads and projects, chat, approve/stop, send annotations | Automatic reconnect to T3 *(gap)* |
| Agent plugins inherited from revdiff | aidiff-branded plugins *(gap)* |

## 6. Business requirements

Each requirement has an ID for traceability. [FUNCTIONAL_SPECIFICATION.md](FUNCTIONAL_SPECIFICATION.md) gives the detailed behavior.

### Review engine (provided by revdiff)
aidiff does not implement reviewing itself; it relies on revdiff for:
- **BR-1. Review sources** — uncommitted, staged, ref and range diffs in git/hg/jj, file lists, stdin, two-file compare.
- **BR-2. Annotations and output** — line/hunk/file annotations written as structured markdown, exit code 10 when annotations exist, mid-session flush, history auto-save.

Our requirement on it: **we use it unchanged** and keep the ability to merge upstream releases.

### Folder review
- **BR-3. Review without a repository.** The user can run aidiff in any folder and browse/annotate its files. Folders load lazily so very large trees open instantly.

### Live, integrated workspace
- **BR-4. Stay current.** In git repositories, folders and T3 threads, the review refreshes on its own when files change or the agent finishes a step. It keeps the cursor position and every annotation.
- **BR-5. Embedded terminal.** The user can open one or more shell panes beside the review, resize them, and move focus between panes from the keyboard or mouse.
- **BR-6. Command palette.** A single shortcut (ctrl+p) lists the available actions: terminals, T3 connection, threads, chat, quit.
- **BR-7. Safe quit.** Quitting while panes are open asks for confirmation, so running shells or chats are not closed by accident.

### T3 Code
- **BR-8. Connect anywhere.** The user can connect to a local T3 server, a remote one over SSH (the tunnel is set up automatically), or one at an HTTP(S) URL, with token login done automatically where possible and pairing otherwise.
- **BR-9. Review agent threads.** The user can review the changes of a T3 thread, either its branch range or its uncommitted changes, and switch between threads without restarting.
- **BR-10. Chat with the agent.** The user can read the transcript, send and queue messages, attach images, answer approvals and questions, change model, reasoning effort and runtime mode, and see whether the agent is working, waiting or idle, with subagent and process counts.
- **BR-11. Controlled interruption.** Stopping an agent run always takes explicit confirmation, and stops only the run the user was asked about.
- **BR-12. Remote shells.** When the T3 session goes through SSH, new terminal panes open on that host, in the thread's working directory.
- **BR-13. Manage work.** The user can create projects (by browsing the server's filesystem) and new threads from the palette.

### Operability
- **BR-14. Configurable.** Every aidiff option can be set by CLI flag, environment variable or config file, alongside revdiff's.
- **BR-15. Agent-tool compatibility.** The revdiff launchers and plugins for Claude Code, Codex, OpenCode and pi, with their terminal overlay backends, keep working against the same flags, output and exit codes.
- **BR-16. Fast startup and interaction.** Startup must not stall on terminals that don't answer color queries, and terminal panes must feel native. See [TECHNICAL_REQUIREMENTS.md](TECHNICAL_REQUIREMENTS.md#performance).

## 7. Success criteria

| Criterion | Measure |
|---|---|
| Review loop is fast | From "agent finished" to "annotations delivered" takes no window switch and no copy-paste in T3 mode. |
| Live refresh is trustworthy | After a refresh, the cursor stays on the same content and no annotation is lost. |
| revdiff stays mergeable | Upstream releases merge without touching fork packages beyond the documented hooks. |
| Terminal feels native | Typing in a terminal pane has no visible lag and does not redraw the review. |
| Startup | No 5-second stall on terminals that don't answer OSC queries. |

## 8. Assumptions and constraints

- Users work on Linux or macOS in a terminal with true color and, ideally, mouse support.
- T3 Code's RPC protocol is internal to T3 and can change. aidiff pins orchestration protocol version 2 (see [T3_INTEGRATION.md](T3_INTEGRATION.md)).
- SSH access to remote T3 hosts uses key or agent auth. The tunnel runs in batch mode and cannot prompt for a password.
- aidiff is a fork and regularly merges upstream revdiff. Fork code is kept in separate packages (`splitview`, `termpane`, `watch`, `t3client`, `chat`, `t3chat`, `t3palette`, `clipboard`, `imagefile`) to keep merges cheap.
