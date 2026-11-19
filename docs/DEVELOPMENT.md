# Development

## 1. Setup

- Go 1.26. Dependencies are vendored (`vendor/` is committed). After changing them, run `go mod vendor`. The patched termenv (§5) comes from `third_party/termenv` through a `replace`, so re-vendoring keeps the patch.
- `nix develop` gives a shell with Go (`flake.nix`).
- The project conventions in [`CLAUDE.md`](../CLAUDE.md) apply.

## 2. Commands

| Command | Does |
|---|---|
| `make build` | Builds `.bin/revdiff` with `-X main.revision=<branch>-<hash>-<timestamp>` |
| `go build -o .bin/aidiff ./app/revdiff` | Builds the binary under the aidiff name, as used locally |
| `make test` | `go test -race` with coverage, mocks excluded |
| `make race` | `go test -race -timeout=180s ./...` (matches CI) |
| `make lint` | `golangci-lint run` |
| `make fmt` | Formats the code |
| `go generate ./...` | Regenerates moq mocks |

## 3. Where aidiff code lives

| Package | What |
|---|---|
| `app/splitview` | Root model: panes, palette, quit guard, focus, refresh gating |
| `app/termpane` | Embedded terminal |
| `app/watch` | Live refresh |
| `app/t3client` | T3 Code client |
| `app/chat` | Chat pane, a library behind its `Backend` interface; knows no agent system |
| `app/t3chat` | The chat pane's T3 backend: a thread's events as conversations, T3's commands |
| `app/t3palette` | T3 palette commands: sessions, thread reviews, chat panes |
| `app/clipboard` | Linux clipboard |
| `app/imagefile` | Reads image files attached to chat messages |
| `app/revdiff/t3.go`, `t3_palette.go`, `t3_terminal.go`, `chat.go`, `split.go`, `watch.go`, `source_setup.go`, `review_model.go` | Wiring in the composition root |
| `app/ui/refresh.go`, `folder.go`, `chrome.go`, `identity.go`, `treedrag.go`; `app/ui/sidepane/foldertree.go`, `scroll.go`; `app/diff/folder.go`, `gitmeta.go`, `watchdirs.go` | Our code inside revdiff packages, reached through one-line hooks in revdiff files |
| `third_party/termenv` | Patched termenv (§5) |

Everything else is revdiff. Avoid changing it: each change there becomes a possible conflict on the next upstream merge. If a feature needs the review model to do something new, add a small, explicit hook, like `RefreshMsg` and `CanRefresh()`, and keep the logic in our packages.

## 4. Merging upstream revdiff

1. `git fetch` from the upstream remote (`github.com/umputun/revdiff`) and merge into `main`.
2. Resolve conflicts. They are usually in `app/revdiff/main.go`, `config.go` and `renderer_setup.go`, and in the hook files above.
3. If upstream moved termenv to a new version, update `third_party/termenv` to it and keep the patch (§5).
4. Run `make race` and `make lint`.

## 5. Patched dependencies

termenv v0.16.0 is replaced by the copy in `third_party/termenv` (`replace` in `go.mod`), whose `termenv_unix.go` sets the OSC color-query timeout to **150 ms** instead of upstream's 5 s. `go mod vendor` vendors the copy, patch included; never edit `vendor/` directly, as the next re-vendoring would undo it.

Bubble Tea v1 queries the terminal background in its package initializer, before application code can configure anything. A terminal that doesn't answer OSC queries would delay every launch by 5 seconds. The COLORFGBG/default-color fallback still applies, and responsive terminals still report their real color.

Keep the copy until the dependency bounds the startup query or stops making it during init. `TestStartupSilentTerminal` (`app/revdiff/startup_test.go`) checks startup against a terminal that never answers.

Trade-off: a terminal that answers after 150 ms (a slow SSH link) answers after termenv gave up. The late reply reaches Bubble Tea as input and is parsed as keys: `alt+]`, a runes key `11;rgb:…`, `alt+\`, and an unknown CSI sequence for the cursor report. Panel focus moves with alt+← / alt+→, so the stray `alt+]` reaches the focused panel as a key instead of moving focus.

## 6. Tests

- Every package has unit tests, written with testify. Mocks are generated with moq, never edited by hand.
- aidiff's packages have their own `_test.go` files and run without a server. The chat pane's tests drive it through a fake `chat.Backend` and fake clipboard and image files; the T3 backend's tests use a fake server and a fake `chat.Viewer`, plus a few that drive a real pane over the T3 backend. No test sleeps: they wait on the pane's `Updates` and on what the fakes record.
- **PTY subprocess tests** in `app/revdiff/main_test.go` re-run the test binary under `script(1)` to get a real TTY. `TestStartupSilentTerminal` (`startup_test.go`) does the same through a PTY, with `AIDIFF_SILENT_TERMINAL_HELPER`.
- The inherited plugin and launcher tests (`plugin_exit_code_test.go`) still run, since the plugins ship in this repository.

## 7. CI

`.github/workflows/ci.yml` runs on pushes, tags and PRs:

| Job | Checks |
|---|---|
| `build` | Go 1.26 tests with the race detector and coverage, golangci-lint v2.15.0. hg and jj are installed for the VCS tests. |
| `opencode-v2` | Typecheck and tests for the inherited OpenCode plugin |
| `lint-scripts` | Frontmatter and Codex marketplace validation, and shellcheck v0.11.0 pinned by digest |

## 8. Releases

- Versions are git tags (`v0.0.1` … `v0.0.24`).
- The upstream release workflow was removed (`a7ba920`), and aidiff releases are made separately. `.goreleaser.yml` is still upstream's: it builds a `revdiff` binary for linux and darwin on amd64 and arm64, and publishes to upstream's Homebrew tap. Don't run it as is.
- With each release, update the version badge and `softwareVersion` in `site/index.html`.
