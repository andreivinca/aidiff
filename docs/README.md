# aidiff documentation

aidiff is a terminal workspace for reviewing what coding agents change. It is built on [revdiff](https://github.com/umputun/revdiff), which provides the review engine: diffs, annotations, themes and review keys. aidiff adds the workspace around it: terminal panes, a command palette, live refresh, folder browsing, and a [T3 Code](https://github.com/pingdotgg/t3code) client with an agent chat.

**Scope.** These docs cover aidiff's own features and how it uses revdiff. They do not explain how revdiff works internally; for that, see the upstream project. The content was written from the code at tag `v0.0.8`.

| Document | Contents |
|---|---|
| [BUSINESS_REQUIREMENTS.md](BUSINESS_REQUIREMENTS.md) | Problem, vision, users, goals, scope, requirements, success criteria |
| [FUNCTIONAL_SPECIFICATION.md](FUNCTIONAL_SPECIFICATION.md) | What revdiff provides, aidiff features, keys, user flows |
| [TECHNICAL_REQUIREMENTS.md](TECHNICAL_REQUIREMENTS.md) | Platforms, dependencies, non-functional requirements, limits |
| [ARCHITECTURE.md](ARCHITECTURE.md) | Our packages, how revdiff is wrapped, data flows, design decisions |
| [CONFIGURATION.md](CONFIGURATION.md) | aidiff flags, env vars, files, fixed keys |
| [T3_INTEGRATION.md](T3_INTEGRATION.md) | Connecting, auth, protocol, thread review, chat pane |
| [INTEGRATIONS.md](INTEGRATIONS.md) | How annotations get out, and the inherited agent plugins |
| [SECURITY.md](SECURITY.md) | Trust model, protections, known risks |
| [DEVELOPMENT.md](DEVELOPMENT.md) | Build, test, upstream merges, patched dependencies, CI, releases |
| [KNOWN_ISSUES.md](KNOWN_ISSUES.md) | Gaps, limitations, unfinished rename |

## Naming

The product is **aidiff**, but the Go module, the build output, the config directory (`~/.config/revdiff/`) and the env vars (`REVDIFF_*`) still use the revdiff name. These docs say `aidiff` for the command and use the real paths and variable names everywhere else. See [KNOWN_ISSUES.md](KNOWN_ISSUES.md#branding).
