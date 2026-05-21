# Changelog

All notable changes to atlas are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.2.0] - 2026-05-20

### Added

- New `worktree` grouping mode (in the `tab` cycle) that clusters a
  project's linked worktrees under its primary checkout as a tree:
  the primary anchors a subtree, linked worktrees render as
  indented children with `├─` / `└─` connectors. The parent stays
  above its children under every sort key.
- **Lagging-worktree signal** — `⊘` flag and `lagging worktree`
  highlight on a checkout whose `LastCommitAt` is ≥ `stale_days`
  behind its project's freshest worktree (or has no commits while
  a sibling does). Reuses `stale_days`; no new config. `⊘`
  suppresses `▲` on the same row since lagging implies absolute
  stale.
- Status-bar `N lagging` count alongside the existing
  dirty / ahead / behind / stash / stale parts.
- Detail-pane **worktree roster** — siblings of the selected
  project listed with branch · relative-last-commit · activity
  tier, plus `▲` / `⊘` flags and a `(primary)` tag. Surfaces the
  full project across every grouping mode, not just `worktree`.
- **Rolled-up lagging marker** on a project's primary row when any
  child worktree lags, so a forgotten checkout scrolled off-screen
  is still discoverable from the anchor.
- **Docked flag legend** in the bottom-right of the detail pane —
  a permanent key for the column glyphs (`* ? ▲ ⊘ ! ↑N ↓N ≡N`).
  Collapses when the terminal is too short for detail + spacer +
  legend.
- `?` help overlay now lists the flag glyphs and renders keybinds
  in two columns when the terminal is wide enough, falling back to
  one column on narrow widths so the overlay never overruns the
  terminal edge. Keys are bolded for visual separation from their
  descriptions.

[0.2.0]: https://github.com/sethdeckard/atlas/releases/tag/v0.2.0

## [0.1.0] - 2026-05-14

First public release. atlas is a smart, automatic map of every Git
repository under your projects root — what's there, where, what state
it's in, and what's worth your attention right now. Single static
binary; TUI-first with full CLI parity for pipelines.

### Added

#### TUI

- Bubble Tea-rendered repo table scoped to the configured projects
  root, with live-streaming refresh and instant warm launches from
  the cache.
- Vim-style navigation: `j` / `k`, `g` / `G`, `ctrl+u` / `ctrl+d`;
  arrow keys, `home`, `end` work as aliases.
- Live fuzzy filter (`/`) on the repo column; `esc` clears the
  active filter without quitting.
- Cycle sort key with `s`, reverse with `S`; cycle grouping with
  `tab` (activity → top-dir → language → none). Last choice is
  persisted in the cache and restored on the next launch.
- Open the selected repo: `enter` prints its path on stdout and
  exits (pairs with a shell `cd` wrapper documented in the README);
  `c` copies the path to the clipboard; `o` opens the origin URL
  in the browser.
- `r` triggers a full refresh; `?` shows a help overlay listing
  every binding; `q` quits and writes the cache.
- Detail pane on the right when the terminal is ≥ 100 columns
  wide, including a plain-English "Highlights" line summarizing
  the table glyphs.
- Two themes: `default` (periwinkle on dark-navy truecolor) and
  `ansi` (terminal's 16-color palette, blends with your shell
  theme).

#### CLI subcommands

- `atlas [PATH]` — launches the TUI; falls back to `atlas list`
  when stdout isn't a terminal so `atlas | cat` works in pipelines.
- `atlas list [PATH] [flags]` — table / `name` / `json` output.
  Filters: `--dirty`, `--top-dir`, `--language`. Sort:
  `--sort=repo|last_commit_at`, `--reverse`. Cache control:
  `--cached` (skip git entirely), `--fresh` (force re-read).
  Pagination: `--limit`.
- `atlas export --markdown PATH` — render the repo set as a
  markdown document, sectioned by activity tier and sub-sectioned
  by top-level directory. Sections with more than 20 repos
  collapse under `<details>`.
- `atlas refresh [-v]` — force a full re-read of every repo into
  the cache. Quiet by default; `--verbose` prints one line per
  repo whose state changed.
- `atlas init` — interactively prompts for the projects root and
  saves it to the config file. Existing values are preserved.
- `--cd` flag on the root command reserves stdout for the selected
  repo path so a shell wrapper can `cd` into it cleanly.

#### Derived per-repo signals

- **Languages** detected from manifests at the worktree root
  (`go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`,
  `Gemfile`, `pom.xml`, `Package.swift`, and more).
- **Behind / ahead vs upstream** — `git rev-list` against the
  resolved `@{u}`, no network calls. Reflects your last manual
  fetch state. Rendered as `↑N` / `↓N` glyphs.
- **Stash count** from `git stash list` (`≡N` glyph).
- **Branch count** — local branches, loose plus packed.
- **Commits in the last 30 days** as a recent-activity gauge.
- **Activity tier** — `recent` (< 14d) → `active` (< stale_days)
  → `cold` (< 365d) → `dormant` (≥ 365d) → `empty`. The
  active/cold boundary tracks `stale_days` so the tier never
  contradicts the stale flag.
- **Stale flag** (`▲` glyph) for repos older than `stale_days`.
- **Worktree count** — linked worktrees share project identity
  and each appears as its own row with independent state.

#### Configuration

- XDG-style config at `$XDG_CONFIG_HOME/atlas/config.toml`
  (default `~/.config/atlas/config.toml` on every OS, including
  macOS — atlas ignores `~/Library/Application Support`).
- Self-documenting config: every defaultable key is rendered as
  a commented `# atlas:default <key> ... # atlas:end` block
  showing the current built-in default. atlas refreshes commented
  blocks on every launch so the next uncomment lands on
  up-to-date defaults; uncommented values are never touched.
- Keys: `root` (written by `atlas init`; no implicit default),
  `max_depth` (default 6), `skip_dirs` (bare names or absolute
  paths; uncommenting replaces the built-in defaults entirely),
  `stale_days` (default 60), `theme` (`"default"` or `"ansi"`).
- Non-fatal validation issues surface as `warning:` lines on
  stderr at launch; the TUI also shows a count in the status bar
  and lists each in the `?` help overlay.

#### Cache

- XDG-style cache at `$XDG_CACHE_HOME/atlas/cache.json` (default
  `~/.cache/atlas/cache.json` on every OS). Global, keyed by
  absolute repo path. Running atlas from `~/projects/go` or from
  `~/projects` shares one cache; only what's needed for the
  active subtree is refreshed.
- Mtime-fingerprinted entries for git-state fields; warm launches
  re-read only what changed. Missing or corrupt caches trigger
  a clean rebuild. Schema version drops incompatible caches on
  load.

#### Distribution

- Single static binary for `darwin/amd64`, `darwin/arm64`,
  `linux/amd64`, and `linux/arm64`.
- Homebrew cask via `brew install sethdeckard/tap/atlas`. The
  cask's post-install hook strips `com.apple.quarantine` on
  macOS so the unsigned binary runs immediately.
- `go install github.com/sethdeckard/atlas/cmd/atlas@latest`
  for users with a Go toolchain.

### Notes

- atlas is **observability-only**: it reads from disk and `git`,
  and never invokes mutating commands (`fetch`, `pull`, `push`,
  `clone`). There is intentionally no `atlas open <name>`
  subcommand — the TUI's `enter` paired with the shell `cd`
  wrapper is the canonical way to land in a repo.
- atlas writes **only** to `~/.config/atlas/config.toml` and
  `~/.cache/atlas/cache.json` (or their XDG overrides). No
  `.atlas.toml` in any repo; no atlas-managed metadata anywhere
  else under `$HOME`.

[0.1.0]: https://github.com/sethdeckard/atlas/releases/tag/v0.1.0
