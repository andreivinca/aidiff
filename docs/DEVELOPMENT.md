# Development

## 1. Setup

- Go 1.26. Dependencies are vendored (`vendor/` is committed). After changing them, run `go mod vendor`, then **reapply the vendor patch** (§5).
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
| `app/t3chat` | Chat pane |
| `app/clipboard` | Linux clipboard |
| `app/revdiff/t3.go`, `t3_palette.go`, `t3_terminal.go`, `watch.go` | Wiring in the composition root |
| `app/ui/refresh.go`, `app/ui/folder.go`, `app/diff/folder.go`, `app/diff/watchdirs.go` | Hooks inside revdiff code |

Everything else is revdiff. Avoid changing it: each change there becomes a possible conflict on the next upstream merge. If a feature needs the review model to do something new, add a small, explicit hook, like `RefreshMsg` and `CanRefresh()`, and keep the logic in our packages.

## 4. Merging upstream revdiff

1. `git fetch` from the upstream remote (`github.com/umputun/revdiff`) and merge into `main`.
2. Resolve conflicts. They are usually in `app/revdiff/main.go`, `config.go` and `renderer_setup.go`, and in the hook files above.
3. If `vendor/` changed, reapply the termenv patch (§5).
4. Run `make race` and `make lint`.

## 5. Vendor patches

`vendor/github.com/muesli/termenv/termenv_unix.go`: the OSC color-query timeout is **150 ms** instead of upstream's 5 s.

Bubble Tea v1 queries the terminal background in its package initializer, before application code can configure anything. A terminal that doesn't answer OSC queries would delay every launch by 5 seconds. The COLORFGBG/default-color fallback still applies, and responsive terminals still report their real color.

Reapply this patch after every `go mod vendor` until the dependency bounds the startup query or stops making it during init. `TestStartupSilentTerminal` (`app/revdiff/main_test.go`) checks startup against a terminal that never answers.

## 6. Tests

- Every package has unit tests, written with testify. Mocks are generated with moq, never edited by hand.
- aidiff's packages have their own `_test.go` files. They use injectable `Config` hooks (`Dispatch`, `Subscribe`, `Catalog`, `Clipboard`, …), so T3 chat tests run without a server.
- **PTY subprocess tests** in `app/revdiff/main_test.go` re-run the test binary under `script(1)` to get a real TTY. Among them is `TestStartupSilentTerminal`, which uses `AIDIFF_SILENT_TERMINAL_HELPER`.
- The inherited plugin and launcher tests (`plugin_exit_code_test.go`) still run, since the plugins ship in this repository.

## 7. CI

`.github/workflows/ci.yml` runs on pushes, tags and PRs:

| Job | Checks |
|---|---|
| `build` | Go 1.26 tests with the race detector and coverage, golangci-lint v2.15.0. hg and jj are installed for the VCS tests. |
| `opencode-v2` | Typecheck and tests for the inherited OpenCode plugin |
| `lint-scripts` | Frontmatter and Codex marketplace validation, and shellcheck v0.11.0 pinned by digest |

## 8. Releases

- Versions are git tags (`v0.0.1` … `v0.0.8`).
- The upstream release workflow was removed (`a7ba920`), and aidiff releases are made separately. `.goreleaser.yml` is still upstream's: it builds a `revdiff` binary for linux and darwin on amd64 and arm64, and publishes to upstream's Homebrew tap. Don't run it as is.
- With each release, update the version badge and `softwareVersion` in `site/index.html`. It currently says `0.0.4`, which is behind the latest tag.
