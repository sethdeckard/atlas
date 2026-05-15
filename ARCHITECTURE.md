# atlas — Architecture

This is a contributor-facing tour of how atlas is shaped today. For
the daily-work conventions (code style, testing patterns, commit
messages), see [AGENTS.md](AGENTS.md). For user-facing docs see
[README.md](README.md).

## Overview

atlas is a Go TUI tool that maps every Git repository under a chosen
root. It ships as a single static binary with two main entry
surfaces:

- **TUI** (`atlas`) — Bubble Tea full-screen app: filterable,
  sortable, group-able table with a detail pane.
- **CLI** (`atlas list` / `atlas export` / `atlas refresh`) —
  scriptable subcommands that share a backbone with the TUI.

The defining constraint is that **atlas is observability-only**. It
surfaces what `git` and the filesystem already say (language, dirty
state, upstream divergence, stash count, branch count, recent
cadence, activity tier) and never asks the user to curate or tag
anything. There is no atlas-managed metadata file in user home or
inside repos.

A JSON cache makes warm launches feel instant: the cache renders
immediately, and a background refresh stream updates rows in place
as fresh data arrives.

## Top-level layering

```
cmd/atlas/main.go
        │
        ├──► internal/cli   (cobra subcommands; Pipeline backbone)
        │         └──► internal/cache, /repo, /scan, /git, /config,
        │              /onboard
        │
        └──► internal/tui   (Bubble Tea root model + helpers)
                  └──► internal/cache, /repo, /scan, /git, /editor,
                       /sysopen, /onboard
```

Per package:

| Package | Role |
| --- | --- |
| `cmd/atlas/main.go` | Cobra root command. Sets up the SIGINT/SIGTERM-aware context, registers subcommands, and dispatches: TUI when stdout is a TTY, fall back to `list` when piped. Owns `version`/`commit`/`date` ldflag vars. |
| `internal/cli` | Cobra subcommands. `cli.Pipeline` owns the shared discover→reconcile→refresh→annotate backbone; `list`/`export`/`refresh` are filter+sort+render layers on top. |
| `internal/tui` | Bubble Tea root `Model`, table renderer, detail pane, filter input, group/sort cycling, key bindings, lipgloss styles, and the cache save coordinator. |
| `internal/cache` | JSON cache with mtime-fingerprint invalidation. Atomic save (tempfile + rename). Worker-pool helpers (`Refresh`, `RefreshStatus`) consumed by both CLI and TUI. Schema versioning. |
| `internal/repo` | The `Repo` aggregate (the value the rest of atlas operates on), the per-repo reader, and a small set of derived-signal helpers (`Sort`, `TopDir`, `PathUnderRoot`, `Highlights`, `AnnotateDerived`, `ClassifyActivity`, `DetectLanguages`). |
| `internal/scan` | Filesystem walk that discovers repo paths under a root, honoring skip dirs and max depth. |
| `internal/git` | Narrow `os/exec` wrapper. `Paths` resolver handles normal/worktree/bare layouts uniformly; helpers (`Head`, `Status`, `LastCommit`, `OriginURL`, `DefaultBranch`, `ResolveUpstream`, `BehindAhead`, `StashCount`, `BranchCount`, `CommitsLast30d`, `RecentCommits`) take `(ctx, Paths)`. **Local-only invariant** enforced in the package doc. |
| `internal/config` | TOML config parser (`~/.config/atlas/config.toml`). `Load` returns `(Config, []string, error)` so non-fatal validation issues surface as warnings the CLI prints to stderr and the TUI shows in its status bar. Also exports `ExpandHome` / `ContractHome` so paths render with `~` prefixes consistently across CLI, TUI, and the onboarding prompt. |
| `internal/sysopen` | Cross-platform browser launcher (`open`/`xdg-open`/`rundll32`) plus URL conversion (SCP-form `git@host:owner/repo.git` → `https://`). `Opener` is a package var so tests skip the real browser. |
| `internal/onboard` | First-run prompt for the projects root. The fallback when no `[PATH]`, no `--root`, and no `root:` config value supplies one; the answer is persisted via `config.Save`. Used by both the CLI pipeline and the TUI launcher; depends only on `internal/config` + stdlib. |
| `internal/gitfixture` | Test helper that builds tiny real Git repos under `t.TempDir()` via `os/exec`. Pins author/committer dates so commit SHAs are stable. |

## Key data flows

### Cold launch (no cache)

1. `tui.Run` (or `list.runList`) loads config + opens the empty cache.
   If no root is supplied — no `[PATH]`, no `--root`, no `root:` in
   config — control routes through `internal/onboard`, which prompts
   the user, validates the directory, and persists the answer via
   `config.Save`. In a no-TTY context (`atlas list | cat`) the
   onboarding step errors with a message pointing at `atlas init`.
2. The TUI renders an empty table and dispatches `discoverCmd`.
3. `scan.Discover` walks the root and produces a path slice.
4. `reconcileCache` classifies every path as stale (since the cache
   has nothing).
5. `runFullRefresh` fans the path list across an 8-worker pool that
   calls `repo.Read`. Each completion arrives as a
   `repoRefreshedMsg` and updates the visible row in place.
6. After the stream drains, `repo.AnnotateDerived` stamps the
   transient signals (activity tier, stale flag, worktree count)
   over the full scoped repo set.
7. The cache saves atomically; a periodic save also triggers every
   N refreshes so a `kill -9` mid-stream loses at most that many.

### Warm launch (cache populated)

1. Cache loads from disk; the TUI renders cached rows immediately.
2. `discoverCmd` walks again.
3. `reconcileCache` partitions discovered paths into
   `(stale, statusOnly, gone)`. Stale entries (mtime mismatch on any
   fingerprint) get a full re-read; status-only entries (still
   matching mtimes) get a cheap `git status` pass that also
   recomputes the bucket-2 last-known signals (Languages,
   StashCount, BranchCount, CommitsLast30d). Gone entries (cached
   but missing on disk) are deleted from the cache.
4. Both refreshes stream `repoRefreshedMsg` back into the model;
   `AnnotateDerived` runs after every cache mutation so transient
   signals stay coherent.

### `atlas refresh`

1. `NewPipeline(Fresh: true)` loads cache + resolves root.
2. `Run` calls `scan.Discover`, then `reconcileCache` — which
   prunes gone-from-disk entries unconditionally, then forces
   every discovered path into the full-Read bucket.
3. The worker pool drains; `AnnotateDerived` runs.
4. `--verbose` diffs the pre-refresh cache snapshot against the
   post-refresh repo set and prints one line per change
   (`dirty +/-`, `branch X → Y`, `last_commit_at +/-`,
   `ahead`/`behind`/`stashes`/`branches`/`languages`,
   `(new)`/`(removed)`).
5. The cache saves.

## The `Repo` aggregate (three buckets)

`internal/repo/repo.go` defines `Repo`, the value every layer
operates on. M4 derived-signal fields fall into three buckets, each
with a different persistence + refresh contract:

1. **Persisted + git-fingerprinted** — `BehindOrigin`,
   `AheadOrigin`, `UpstreamRef`. These depend only on git refs, and
   the `RefsRemotesMtime` / `UpstreamRefMtime` / `PackedRefsMtime`
   fingerprints catch every change. Cache loads them as-is; warm
   path updates them only when fingerprints mismatch.
2. **Persisted as last-known** — `Languages`, `StashCount`,
   `BranchCount`, `CommitsLast30d`. The mtime fingerprint set can't
   catch wall-clock drift (a 29-day-old commit becoming 30+ days
   old without any git change), manifest changes at the worktree
   root (no tracked git mtime), or nested refs (`refs/heads/feature/foo`
   doesn't bump the parent dir mtime). The warm-path status pass
   (`repo.UpdateStatus`) **unconditionally** recomputes them.
   `--cached` renders the last-cached value with stale-by-design
   semantics — the same contract `Dirty` already had.
3. **Transient** (`json:"-"`) — `ActivityTier`, `Stale`,
   `WorktreeCount`. Pure functions of persisted fields plus config
   plus the full repo set. Populated by `repo.AnnotateDerived` on
   every load.

**Adding a new derived signal: pick a bucket.** If the value can
drift without a git mtime change, it's bucket 2 — extend
`UpdateStatus`. If it's a pure function of already-persisted fields
plus config, it's bucket 3 — extend `AnnotateDerived`. Otherwise
add a fingerprint and put it in bucket 1.

## Cache architecture

`$XDG_CACHE_HOME/atlas/cache.json`. The schema is:

```json
{ "version": 3, "repos": { "<abs path>": <Repo>, ... } }
```

- **Global, abs-path keyed.** Filtering by the active root happens
  at read time, so running atlas from `~/projects/go` and from
  `~/projects` shares one cache and only refreshes what's needed
  for the active subtree.
- **Mtime fingerprints.** `cache.Validate` re-stats six paths per
  repo (HEAD, index, config, refs/remotes, packed-refs, the
  resolved upstream ref) and reports stale + gone. The reader
  stamps these fingerprints; the cache compares them.
- **`repo.PathUnderRoot`** is the canonical "is this path under
  root" predicate. Three earlier duplicates (`cache.pathUnderRoot`,
  `cli.scopedRepos`, `refresh.snapshotByPath`) all route through it
  so the semantics agree exactly and the check stays separator-
  aware on Windows.
- **Atomic save.** Tempfile in the same dir + `os.Rename`. The TUI
  drives saves through the `saveCoordinator` (`tui/save.go`):
  at most one save in flight, latest snapshot queued behind it.
  `Cache.Snapshot()` deep-copies at request time so an async
  marshal can never race a concurrent map write.
- **Schema versioning.** `CurrentVersion` is bumped on
  incompatible changes; mismatched on-disk caches are dropped on
  load (cold rebuild). No migration shim — cache loss is always
  recoverable.
- **Best-effort.** Missing or corrupt cache files just trigger a
  cold rebuild; the binary never refuses to launch over cache
  state.

## Config architecture

`internal/config` parses `~/.config/atlas/config.toml` (or
`$XDG_CONFIG_HOME/atlas/config.toml`), layered over
`config.Defaults()`. `Load` returns
`(Config, []string, error)`: the warnings slice carries non-fatal
validation issues (unknown theme, malformed `skip_dirs` entries),
which the CLI prints to stderr and the TUI surfaces on stderr at
startup plus inside the `?` help overlay.

### Self-documenting config blocks

`internal/config/init_toml.go` ships a small DSL that lets atlas
evolve its built-in defaults without users ever editing their
config:

- `managedKeys` enumerates the defaultable fields:
  `max_depth`, `skip_dirs`, `stale_days`, `theme`. Adding a future
  managed key means extending this slice plus the `switch`
  statements in `renderManagedBlock`,
  `renderManagedUncommented`, and `isManagedKeyAtDefault`. The
  test `TestRenderInitTOML_AllDefaultsAreCommented` iterates
  `managedKeys`, so a new entry is automatically covered.
- `WriteInitTOML` (called by `atlas init` and onboarding) renders
  a fresh config: each managed key whose value matches the
  built-in default appears as a commented sentinel block —
  `# atlas:default <key> — uncomment to override` ... `# atlas:end`
  — with the current default value embedded inside. Keys the user
  has already set are emitted uncommented.
- `refreshManagedDefaults` (called from every successful `Load`)
  rebuilds those sentinel blocks against today's defaults.
  Idempotent: if the rendered text equals what's on disk, no
  write happens; otherwise the file is rewritten atomically.
  User-uncommented keys keep their `userSet[key] = true` entry,
  which short-circuits the rewrite for that key — values the user
  edited are never touched.

This is what lets us widen `BuiltinSkipDirs` or change a default
between releases without leaving every existing user with a stale
example block.

### Theme dispatch

Two themes ship today; the value lives in `Config.Theme`:

- `ThemeDefault` (`"default"`) — periwinkle accents on dark navy,
  truecolor.
- `ThemeANSI` (`"ansi"`) — ANSI 16-color indices, follows the
  user's terminal palette.

`NormalizeTheme` canonicalizes case and whitespace and returns
`""` for unrecognized values. `finalize` (called inside `Load`)
treats unknown values as a warning + reset to the default; an
empty `Theme` falls back silently. `internal/tui/styles.go`'s
`newStyles` is the dispatch point — it switches on the normalized
name and returns the per-theme `styles` struct. Adding a new
theme means: a new constant in `config.go`, a clause in
`NormalizeTheme`, a constructor in `styles.go`, and a clause in
`newStyles`.

### Skip entries

`skip_dirs` accepts two entry shapes; `parseSkipEntries` (in
`config.go`) splits a single user list into the two sets that
`scan.Discover` actually consumes:

- **Bare basename** (no separator) → matches any directory with
  that name, anywhere in the walk.
- **Absolute path** (starts with `/`) or **home-anchored**
  (`~/path` or bare `~`) → matches exactly one absolute location.
  `~` expands against the runtime `$HOME` so a single config
  works across machines with different home paths.

Other shapes (relative paths with separators) become warnings and
are dropped. Uncommenting `skip_dirs` **replaces**
`scan.BuiltinSkipDirs` entirely — no merge — which is why the
self-documenting block emits the full grouped default list so the
user can copy it as a starting point.

## TUI architecture (Bubble Tea / MVU)

`internal/tui` follows the standard Bubble Tea Model-View-Update
pattern with a few atlas-specific contracts:

- **`Update` is the only mutation point.** `View` is a pure
  function of the model; `Init` runs once but has a value receiver,
  so any state it changes is lost on return — see the kickoff
  pattern below.
- **All I/O lives in `tea.Cmd` functions.** `internal/tui/commands.go`
  wraps every git/cache/scan call so the model stays
  single-threaded. Side effects come back as messages.
- **Generation counters for supersession.**
  - The refresh state machine carries `refreshGen` on every msg;
    older-gen messages drop on arrival when the user kicks off a
    new refresh mid-stream.
  - The detail pane's recent-commits load carries `recentGen`;
    fast j/k scrolling supersedes earlier ticks so the model
    doesn't spawn a `git log` shellout per row.
- **`recentCommitsState` lifecycle** (`tui/messages.go`). The
  detail-pane lazy load uses an explicit struct with
  `loading`/`loaded`/`lines`/`err` fields rather than overloading a
  `[]string` value. Missing map key = never requested; `loading: true`
  = fetch in flight; `loaded: true` = have an answer (which may be
  empty, populated, or carry an error). The renderer branches on
  these four states.
- **`rebuildRepos` returns `tea.Cmd`.** When a rebuild lands
  selection on a different repo (cold launch from cached repos,
  sort flip, filter rebuild, refresh-driven reorder), the returned
  cmd schedules the recent-commits load. Callers in `Update` MUST
  batch it.
- **Init can't mutate the model.** Warm-launch detail-pane
  scheduling routes through `initialLoadMsg`: `Init` dispatches a
  cmd that emits the kickoff, and `Update`'s handler does the
  actual `scheduleRecentCommitsLoad` against the live model.
  Generation-dependent setup at startup must follow the same
  pattern.
- **`scheduleRecentCommitsLoad` always bumps `recentGen`** —
  even when the new selection lands on an already-cached repo.
  Otherwise an in-flight tick from a previous selection still
  matches the unchanged gen and runs `git log` for a repo the user
  has already moved away from.
- **Pipe fallback.** When stdout isn't a TTY (`atlas | cat`),
  `cmd/atlas/main.go` redirects to `cli.NewListCommand` instead of
  trying to draw the TUI into a pipe.

## CLI architecture

`internal/cli/pipeline.go` defines `Pipeline`, the shared backbone
every cache-consuming subcommand runs through:

```
NewPipeline ──► load config + warnings → resolve root → load cache
            ──► (--cached short-circuit returns scoped+annotated)
            └─► Run: discover → reconcile → refresh stale →
                status pass over the rest → AnnotateDerived →
                return scoped+annotated repo set
            ──► Save: atomic write
```

Subcommands are filter+sort+render layers on top:

- `list` — applies `--dirty` / `--top-dir` / `--language` /
  `--limit`, sorts via `repo.Sort`, renders as table / name / json.
- `export` — sorts, groups by activity tier + top dir, renders
  markdown with `<details>` collapsing for sections > 20 repos,
  atomic write.
- `refresh` — runs the pipeline with `Fresh: true`, captures a
  pre-refresh snapshot, and (when `--verbose`) prints per-repo
  diffs between snapshots.

atlas intentionally does **not** ship an `atlas open <name>`
subcommand — the TUI's `enter` is the canonical way to open a repo
in `$EDITOR`. atlas is observability-only; a CLI launcher would be
a management/navigation surface.

## Invariants

- **No network calls.** Every helper in `internal/git` is local-
  only — no `git fetch`, `git pull`, `git push`, `git clone`. The
  package doc enforces this. `BehindAhead` reflects the user's
  most recent local fetch state.
- **The reader is the boundary that swallows hard errors.**
  `repo.Read` never returns an `error`; per-record failures land
  in `Repo.Err`. Callers (cache, CLI, TUI) check the field but
  never receive errors from the reader.
- **`repo.PathUnderRoot` is the canonical scoping predicate.**
  Separator-aware (works on Windows). All cache + CLI scoping goes
  through it.
- **Cache is best-effort.** Missing, corrupt, or out-of-version
  caches always trigger a cold rebuild rather than failing to
  launch. Save errors surface as a transient warning and never
  block the UI.
- **`repo.Highlights` is the single source of truth for
  "interesting" wording.** Table glyphs, status-bar count, M5
  detail-pane "Highlights" line, and M6 export markdown all
  consume it. Don't re-decide in renderers.
- **`reconcileCache` always prunes gone entries.** Even on Fresh.
  Otherwise `atlas refresh` would preserve deleted repos forever
  and `--verbose` could never emit `(removed)`.

## Where to find things

| Want to... | Touch |
| --- | --- |
| Add a CLI flag to `list` | `internal/cli/list.go`'s `listOptions` + `cmd.Flags()` registration + `applyFiltersSortLimit`. |
| Add a new TUI key binding | `internal/tui/keys.go` (declare) + `internal/tui/app.go` `handleKey` (route) + `?` overlay auto-includes it from the keymap. |
| Add a new derived signal | Pick a bucket (see Repo aggregate). Bucket 1 → reader + `cache.isStale` fingerprint. Bucket 2 → reader + `repo.UpdateStatus`. Bucket 3 → `repo.AnnotateDerived`. Surface in `repo.Highlights` if it qualifies as "interesting." |
| Add a CLI subcommand | `internal/cli/<name>.go` with a `NewXCommand` factory; build on `cli.NewPipeline` for cache/discovery; register in `cmd/atlas/main.go`. |
| Touch the cache schema | Bump `cache.CurrentVersion`, document the change in the `Version history` comment, add a regression test for "old cache drops on load." |
| Surface a non-fatal config issue | Append to the warnings slice returned by `config.Load`; CLI prints to stderr, TUI accumulates on `Model.warnings`. |

## Cross-references

- [AGENTS.md](AGENTS.md) — code style, commit conventions, testing
  patterns, and a quick-reference summary of the architecture
  rules listed above.
- [README.md](README.md) — user-facing docs (install, key
  bindings, subcommands, config).
