# atlas - Agent Conventions

> Internal development conventions. See README.md for user documentation.

## Project Overview

atlas is a Go TUI tool that maps every Git repository under your
projects root (whichever directory you configure, e.g. `~/projects`).
Built with Bubble Tea (`bubbletea`, `lipgloss`, `bubbles`) for the TUI
and `cobra` for CLI dispatch. Repo state is read live from disk and
Git, with an mtime-invalidated JSON cache making warm launches feel
instant.

## Driving Philosophy

Three principles set atlas apart from most repo dashboards. Every
feature decision and code review should weigh against them:

- **Observability-only.** atlas reads from disk and `git`; it never
  modifies a repo, never asks the user to curate or tag, and never
  shells out to mutating commands (`git fetch`/`pull`/`push`/`clone`).
  See `internal/git`'s package doc for the local-only invariant.
- **Config is minimal and optional.** atlas runs without a config
  file: positional `[PATH]` or `--root` cover the one-off case;
  `root` in the config covers the persistent case; the onboarding
  prompt is only the fallback when no source is supplied. Every
  other knob has a sensible default; resist adding required config
  for new features. Surface non-fatal validation issues as warnings
  rather than errors.
- **No marker files in repos or home.** atlas writes only to
  `~/.config/atlas/config.toml` and `~/.cache/atlas/cache.json` (or
  the XDG overrides). No `.atlas.toml` in any repo, no
  atlas-managed metadata under `$HOME` outside those two paths,
  no hidden state inside `.git`. If a feature needs persistent
  state, it goes in the cache.

For how the system fits together, read
[ARCHITECTURE.md](ARCHITECTURE.md) before starting unfamiliar work.

## Code Style

- Idiomatic Go: follow Effective Go patterns, standard naming.
- Format with `gofmt` / `goimports`.
- Error handling: return errors, don't panic. Wrap with
  `fmt.Errorf("context: %w", err)`.
- No `log.Fatal` in library code (`internal/...`). Only
  `cmd/atlas/main.go` may exit the process.
- Reader-style packages (`internal/repo`) swallow per-record errors
  into `Repo.Err` rather than failing the whole call — callers check
  the field, never receive `error` from `repo.Read`.

## Commit Messages

- Subject: max 50 chars, capitalized, imperative mood
  (e.g. "Add feature" not "Added feature"), no trailing period.
- Blank line between subject and body.
- Body: wrapped at 72 chars, describe what + why, not how.

The `commit-msg` hook enforces the rules. Activate it once after
cloning:

```
make hooks
```

## Project Structure

```
cmd/atlas/main.go              # Entry point — cobra root, ldflag vars
internal/
  cli/
    list.go                    # `atlas list` subcommand
    list_test.go               # CLI behavior + golden tests
    init.go                    # `atlas init` subcommand (onboarding)
  config/
    config.go                  # TOML config; Load returns warnings; Save persists; ContractHome/ExpandHome
    config_test.go
    init_toml.go               # WriteInitTOML + atlas:default sentinel blocks; refreshManagedDefaults keeps them current
    init_toml_test.go
  cache/
    cache.go                   # JSON cache with mtime invalidation
    cache_test.go              # incl. version-bump regression
  git/
    git.go                     # Paths resolver + local-only os/exec
    git_test.go                # M1 helpers
    git_m4_test.go             # M4 helpers
  repo/
    repo.go                    # Repo struct + Kind enum + JSON tags
    reader.go                  # Read: full populate + fingerprints
    status.go                  # UpdateStatus: warm-path refresh
    sort.go                    # Shared comparator (M3)
    topdir.go                  # Root-aware top-dir helper
    languages.go               # Manifest detection (M4)
    activity.go                # ClassifyActivity (M4)
    annotate.go                # AnnotateDerived: WorktreeCount/Stale/ActivityTier
    highlights.go              # Highlights: shared "interesting" labels
    status_warmcache_test.go   # bucket-2 invalidation tests
  scan/
    scan.go                    # Filesystem walk discovering repos
    scan_test.go
  sysopen/                     # M5: cross-platform browser launcher
    sysopen.go                 # OS dispatch (open / xdg-open / rundll32)
    url.go                     # SCP-form / ssh:// → https conversion
  tui/                         # Bubble Tea TUI (M2-M5)
    app.go                     # Root tea.Model
    table.go                   # Table renderer + group bucketing
    detail.go                  # M5 right-pane renderer
    filter.go                  # Fuzzy filter (M3)
    clipboard.go               # Clipboard interface + atotto impl
    commands.go                # tea.Cmd factories
    messages.go                # tea.Msg types
    keys.go                    # bubbles/key bindings
    styles.go                  # lipgloss styles
    save.go                    # save coordinator (single-writer)
    run.go                     # bootstrap + tea.Program launch
    *_test.go                  # teatest snapshots + behavior tests
  onboard/
    onboard.go                 # First-run prompt for projects root; persists to config
  gitfixture/
    fixture.go                 # Test helper: real tiny repos under t.TempDir()
testdata/
  golden/                      # Checked-in expected output of CLI tests
  fixtures/                    # Generated by tests via gitfixture (not checked in)
```

## Commands

- **Build:** `make build` (or `go build ./cmd/atlas`).
- **Test:** `make test` (or `go test ./...`).
- **Race:** `go test -race ./...`.
- **Lint:** `make lint` (or `golangci-lint run ./...`).
- **Format:** `goimports -w .`.
- **Hooks:** `make hooks` (one-time, activates `commit-msg`).

## Testing

- Co-located `_test.go` files.
- Table-driven tests for logic.
- Use `t.TempDir()` for filesystem tests.
- Use `internal/gitfixture` to build small real Git repos in test
  scratch dirs — **never** check fixture trees into the repo.
- Goldens live under `testdata/golden/` (checked in). They are
  *expected output*, not fixture data.
- Pin `TZ=UTC` in `TestMain` and inject a fixed `nowFunc` for any test
  that touches relative-time formatting.
- `gitfixture` defaults commits to a fixed `FixedTime` (Jan 1 2026).
  Tests that need wall-clock-relative behavior (`CommitsLast30d`,
  activity tier boundaries) override via `gitfixture.WithCommitTime`.
- For tests that need an upstream-tracking ref, see
  `internal/git/git_m4_test.go`'s `setupRemoteAndLocal` helper —
  scaffolds a bare "remote" + a local with `origin` + initial push so
  `refs/remotes/origin/main` exists as a loose ref.
- TUI tests inject fakes for sysopen + clipboard:
  - `sysopen.Opener` is a package var → swap with a fake `*exec.Cmd`
    factory to assert the converted URL without launching a browser.
  - `Model.clipboard` is a `Clipboard` interface → use a
    `fakeClipboard` to capture writes without touching the host.

## Architecture Rules

> See [ARCHITECTURE.md](ARCHITECTURE.md) for the full system
> explanation; this section is the conventions-level summary kept
> next to the daily-work rules so a reviewer can skim it at PR
> time.

### Cache + reader (M1)

- The cache is **global**, keyed by absolute repo/worktree path.
  Filter by the active root at read time, not at cache write time —
  `atlas` from `~/projects/go` and from `~/projects` share one cache
  and only refresh what's needed for the active subtree.
- All git shellouts run in `internal/git/` and take `(ctx, git.Paths)`
  — helpers never reach for `.git` themselves.
- `repo.Read` is the boundary that swallows hard errors. Callers
  (cache, CLI, TUI) check `Repo.Err`, never receive `error` from
  `repo.Read`.
- Worktrees of the same project share `CommonGitDir` but each has
  independent dirty/branch/HEAD state — the data model captures both
  via `CommonGitDir` (project-identity key) and per-worktree cache
  keys.

### TUI (M2)

- Terminal / git / cache calls happen in `tea.Cmd` functions, never
  in `Update` / `View`. Renderers stay pure functions of model state.
- Multi-step streams (refresh) carry a generation counter on every
  message so superseded refreshes can be dropped cleanly when the
  user kicks off a new one.
- Async cache writes go through the `saveCoordinator` (`save.go`):
  at most one save in flight, latest snapshot queued behind it.
  `Cache.Snapshot()` is taken at request time, not when the goroutine
  runs, so the marshal can never race a concurrent map write.

### Derived signals (M4)

- `internal/git` is **local-only**. Every helper there reads existing
  state — no `git fetch`, `git pull`, `git push`, or `git clone`.
  `BehindAhead` reflects the user's last manual fetch state.
- `Repo` fields fall into three buckets, documented on the struct:
  1. **Persisted + git-fingerprinted**: `BehindOrigin`,
     `AheadOrigin`, `UpstreamRef`. Invalidated by
     `RefsRemotesMtime`, `UpstreamRefMtime` (the resolved `@{u}`
     path is cached so `cache.Validate` doesn't re-shellout), and
     `PackedRefsMtime`.
  2. **Persisted as last-known**: `Languages`, `StashCount`,
     `BranchCount`, `CommitsLast30d`. The mtime fingerprint set
     can't catch wall-clock drift, manifest changes at the worktree
     root, or nested refs, so the warm-path status pass
     (`repo.UpdateStatus`) **unconditionally** recomputes them.
     `--cached` renders the last-cached value with stale-by-design
     semantics — same contract `Dirty` already has.
  3. **Transient** (`json:"-"`): `ActivityTier`, `Stale`,
     `WorktreeCount`. Pure functions of persisted fields plus
     config; populated by `repo.AnnotateDerived` over the **full
     scoped repo set** (so `WorktreeCount` reflects sibling
     worktrees even when filter hides them).
- Adding a new derived signal: pick a bucket. If the value can drift
  without a git mtime change, it's bucket 2 — extend `UpdateStatus`.
  If it's a pure function of already-persisted fields plus config,
  it's bucket 3 — extend `AnnotateDerived`.
- `repo.Highlights(r)` is the single source of truth for "why is this
  repo interesting" wording. Table glyphs, status-bar counts, the
  M5 detail-pane "Highlights" line, and (M6) export markdown all
  consume it. Don't re-decide in renderers.

### TUI detail pane (M5)

- The view is split when terminal width ≥ 100; below that the detail
  pane is hidden and the table fills the screen.
- `recentCommitsState` (in `messages.go`) is the explicit per-repo
  lifecycle for lazy commit-subject loading: `loading`/`loaded`/
  `lines`/`err`. The map distinguishes "missing key = never
  requested" from `loading=true` (in flight) and `loaded=true` (have
  an answer, possibly empty or with err). The renderer branches on
  this; **don't** overload nil to mean "loading."
- `Model.rebuildRepos()` returns a `tea.Cmd`. Callers in `Update`
  MUST batch it with whatever else they're emitting — when a rebuild
  lands selection on a different repo (sort flip, filter rebuild,
  refresh-driven reorder), the returned cmd schedules the
  recent-commits load. Without that, the detail pane stays at
  "(loading…)" until the user presses j/k.
- **Init can't mutate the model.** `Init` has a value receiver, so
  any state it changes (e.g. bumping `recentGen`) is lost when Init
  returns. Warm-launch detail-pane scheduling routes through
  `initialLoadMsg`: Init dispatches a cmd that emits the kickoff
  message, and `Update`'s handler does the actual
  `scheduleRecentCommitsLoad` against the live model. New
  generation-dependent setup at startup must follow the same pattern.
- `scheduleRecentCommitsLoad` always bumps `recentGen` — even when
  the new selection lands on an already-cached repo. Otherwise an
  in-flight tick from a previous selection still matches the
  unchanged gen and runs `git log` for a repo the user has already
  moved away from.

### Config + warnings

- `config.Load(path) (Config, []string, error)`. Non-fatal validation
  issues come back as warnings alongside the parsed `Config` so
  callers can surface them:
  - **CLI**: prints each warning as `warning: <text>` on stderr
    before any other output.
  - **TUI**: `tui.Run` prints each warning as `warning: <text>` on
    stderr before the alt screen takes over (so the user sees them
    on launch and again after exit), and the model also stashes
    them on `Model.warnings`, surfacing a count in the status bar
    and listing each in the `?` help overlay.
- Sort and grouping are **not** config keys — the TUI persists them
  via `cache.Session`, and CLI invocations take `--sort` / `--reverse`
  flags. If a user has legacy `[sort]` or `group_by` lines in
  `config.toml`, Load emits a warning saying they're inert and safe
  to delete.
- Unknown `theme` values warn and fall back to `default` via
  `finalize` in `config.go` — same warnings channel, same surface.
  Adding a new theme means a constant in `config.go`, a clause in
  `NormalizeTheme`, plus a constructor + `newStyles` clause in
  `internal/tui/styles.go`. See `ARCHITECTURE.md` "Theme dispatch"
  for the full flow.
- Defaultable config keys round-trip through
  `internal/config/init_toml.go` as commented `atlas:default`
  sentinel blocks. `refreshManagedDefaults` rewrites those blocks
  on every successful `Load` so a user's commented example stays
  in sync with current defaults; user-uncommented values are
  never touched. Adding a new defaultable key: extend
  `managedKeys` and the three `switch` statements in `init_toml.go`
  — `TestRenderInitTOML_AllDefaultsAreCommented` then covers it
  automatically.

### CLI subcommand pipeline (M6)

- `cli.Pipeline` factors out the discover → reconcile → refresh →
  annotate sequence shared by `list`, `export`, and `refresh`. New
  subcommands should build on it rather than re-running the steps.
- `reconcileCache` always prunes gone-from-disk entries — even on
  the Fresh path that `atlas refresh` uses. Skipping that step on
  Fresh would preserve deleted repos in the cache forever and break
  the `(removed)` line in `--verbose` output.
