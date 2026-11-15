# aidiff

**aidiff** is based on [revdiff](https://github.com/umputun/revdiff), with an integrated terminal and [T3 Code](https://github.com/pingdotgg/t3code) support.

Run `aidiff` from any folder. In a repository it shows changes; outside a repository it browses the folder's files, with live refresh and the integrated terminal available. Use `--only path/to/file` to open specific files, or `--include` / `--exclude` to filter the file tree.

Outside repositories, folders load lazily: only the top level is read initially. Select a directory and press Enter or click it to expand/collapse it; Right opens it and Left closes it. Live refresh watches only the root and expanded directories. The file picker searches files in expanded directories.

T3 chat shows sent messages above the input while they are sending or queued, then moves them into the conversation when T3 starts processing them. A persistent status line shows whether the agent is working, waiting for you, or idle, with active subagent and process counts. Press Left with an empty input to inspect activity. Counts use live server events without polling.
