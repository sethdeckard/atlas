# atlas

A smart, automatic map of every Git repository under your projects
root (e.g. `~/projects`) — what's there, where, what state it's in,
and what's worth your attention right now. TUI-first; single static
binary.

![atlas demo](demo/atlas.gif)

## Philosophy

atlas is opinionated about what a repo dashboard should and
shouldn't do: **observability-only**, **minimal config**, **no
marker files in your repos**, and **no network**. See
[PHILOSOPHY.md](PHILOSOPHY.md) for the rationale behind each.

Contributor docs: [ARCHITECTURE.md](ARCHITECTURE.md) explains how
the system is shaped. [AGENTS.md](AGENTS.md) covers code style,
testing, and commit conventions.

## Install

Homebrew (recommended):

```
brew install sethdeckard/tap/atlas
```

Or via Go:

```
go install github.com/sethdeckard/atlas/cmd/atlas@latest
```

## Quick start

```
atlas                  # launches the TUI scoped to the configured root
atlas ~/projects       # scoped to a specific subtree
atlas init             # (re)configure the projects root; saves to config
atlas list             # CLI table of every repo (cache-backed)
atlas list --dirty     # only repos with uncommitted changes
atlas list --language go
atlas export --markdown ~/notes/repos.md
atlas refresh -v       # force re-read every repo, print what changed
```

On first launch — when no `[PATH]`, no `--root`, and no `root` in
the config supplies a value — atlas prompts for the directory that
contains your Git projects and saves the answer to
`~/.config/atlas/config.toml`. Subsequent launches skip the prompt.
Pipelines without a TTY (e.g. `atlas list | cat`) error with a
hint to run `atlas init`.

After onboarding, the first launch builds the cache; subsequent
launches feel instant (cache renders immediately, fresh data streams
in behind the scenes).

## TUI

Navigate, filter, sort, group, and open repos without leaving the
terminal. Bindings lean vim; arrow keys / `home` / `end` work as
aliases.

| key             | action                                                  |
| --------------- | ------------------------------------------------------- |
| `j` / `k`       | move selection (also `↓` / `↑`)                         |
| `g` / `G`       | first / last repo (also `home` / `end`)                 |
| `ctrl+u` / `ctrl+d` | half-page scroll                                    |
| `/`             | live fuzzy filter on the visible repo column            |
| `esc`           | clear the active filter (no-op when nothing's filtered) |
| `s` / `S`       | cycle sort key / reverse direction                      |
| `tab`           | cycle grouping (activity → top_dir → language → none)   |
| `enter`         | print the selected repo's path on stdout, then exit     |
| `c`             | copy the path to the clipboard                          |
| `o`             | open the origin URL in the browser                      |
| `r`             | refresh all repos                                       |
| `?`             | help overlay (lists every binding)                      |
| `q`             | quit (saves the cache first)                            |

When the terminal is wide enough (≥ 100 cols), atlas opens a detail
pane on the right with everything it knows about the selected repo,
including a "Highlights" line that translates the table glyphs to
plain words (`dirty · 2 commits ahead · 1 stash · linked worktree`).

atlas remembers the last sort and grouping you used: cycling with
`s`, `S`, or `tab` writes the new state into the cache, and the next
launch picks up where you left off. The `[sort]` / `group_by`
settings in `config.toml` are the *first-run* defaults.

### Use atlas as a `cd` launcher

`enter` exits atlas and prints the selected repo's path on stdout —
nothing else writes there. The TUI itself is rendered on `/dev/tty`,
so a shell wrapper can capture stdout cleanly. The `--cd` flag is
required to bypass the usual "stdout-isn't-a-terminal → fall back to
`atlas list`" guard, since the wrapper substitution makes stdout look
like a pipe.

```sh
atlas() {
    local target
    target=$(command atlas --cd "$@") || return
    [[ -n $target ]] && cd "$target"
}
```

After defining the function, run `atlas` (the shell function), navigate
to the repo you want, and press `enter` — the TUI exits and your shell
lands inside the directory. Running the binary directly (`command atlas`
or `./atlas`) can only print the selected path; it cannot change the
directory of the parent shell. Quitting via `q` writes nothing to
stdout, so `cd` is skipped.

## Subcommands

- `atlas` — launches the TUI. Falls back to `atlas list` when stdout
  isn't a terminal (so `atlas | cat` works in pipelines).
- `atlas list [PATH] [flags]` — print a table / name list / JSON
  dump. Filters: `--dirty`, `--top-dir`, `--language`. Plus
  `--sort=repo|last_commit_at`, `--reverse`, `--limit`,
  `--cached` (skip git, read cache as-is), `--fresh` (force re-read).
- `atlas export --markdown PATH` — write a curated markdown view of
  the repo set, sectioned by activity tier and sub-sectioned by
  top-level directory. Sections with > 20 repos collapse under
  `<details>`.
- `atlas refresh [-v]` — force a full re-read of every repo into the
  cache. Quiet by default; `--verbose` prints one line per repo whose
  state changed.
- `atlas init` — interactively prompt for the projects root and
  save it to `~/.config/atlas/config.toml`. Other settings already
  in the file are preserved (comments are not).

There is intentionally no `atlas open <name>` subcommand — atlas is
observability-only; the TUI's `enter` is the canonical way to land in
a repo (paired with the shell wrapper above).

## Configuration

`$XDG_CONFIG_HOME/atlas/config.toml` (default
`~/.config/atlas/config.toml` on every OS — atlas is XDG-style
end-to-end and ignores `os.UserConfigDir`'s macOS path of
`~/Library/Application Support`). All keys are optional except
`root`, which atlas writes for you on first launch (or via
`atlas init`).

```toml
root = "~/projects"        # written by `atlas init`; no implicit default
# max_depth = 6            # how deep to walk from root
# skip_dirs = [...]        # uncomment to override the built-in defaults
# stale_days = 60          # active/cold boundary for the activity tier
# theme = "default"        # "default" or "ansi"; see Theme below
```

`atlas init` writes a self-documenting config: every defaultable key
is rendered as a commented `# atlas:default <key> ... # atlas:end`
block showing the current built-in default. Uncomment and edit to
override. On every launch, atlas refreshes any commented blocks for
keys still using their defaults, so a future uncomment lands on
the latest defaults — your uncommented values are never touched.

Sort and grouping aren't config — the TUI remembers them in the
cache after each `s` / `S` / `tab`. CLI invocations default to
`--sort=last_commit_at` (descending, override with `--reverse`).

`skip_dirs` accepts two entry shapes:

- **Bare name** (`"node_modules"`, `".cargo"`) — matches any
  directory with this basename, anywhere in the walk.
- **Path** (`"~/Pictures"`, `"/var/cache/foo"`) — matches exactly
  one directory at this absolute path. `~/` expands to `$HOME`.

Uncommenting `skip_dirs` **replaces** the built-in defaults entirely;
the commented block above it shows the current defaults so you can
copy and edit. The defaults span per-project artifacts (`node_modules`,
`vendor`, `target`, …), language toolchain caches (`.cargo`, `.gem`,
`.npm`, `.m2`, `.gradle`, …), editor and AI-tool config dirs (`.vim`,
`.emacs.d`, `.claude`, …), runtime version managers (`.rbenv`,
`.pyenv`, `.asdf`, …), and macOS user folders anchored to `$HOME`
(`~/Library`, `~/Pictures`, …). See your generated config for the
complete current list.

atlas does not read or write any other config file. There is **no**
per-repo `.atlas.toml`, no atlas-managed metadata in your home
directory.

### Theme

Two themes ship today; pick one with `theme = "<name>"`:

- **`default`** (the default) — periwinkle accents on a dark-navy
  panel, rendered with truecolor hex values. Looks the same on any
  terminal that supports 24-bit color.
- **`ansi`** — uses the terminal's ANSI 16-color palette, so the
  look follows whatever colorscheme you've configured (Solarized,
  Gruvbox, etc.). Recommended for basic 16-color terminals or when
  you want atlas to blend with a curated palette.

Unknown values produce a warning at launch and fall back to
`default`.

## Derived signals

atlas computes per-repo signals on every refresh:

- **Languages** — detected from manifests at the worktree root
  (`go.mod`, `package.json`, `Cargo.toml`, `pyproject.toml`,
  `Gemfile`, `pom.xml`, `Package.swift`, etc.).
- **Behind / ahead vs upstream** — `git rev-list` against the
  resolved `@{u}`, no `git fetch`. Reflects your last manual fetch
  state. `↑N` / `↓N` glyphs in the table.
- **Stash count** — number of `git stash list` entries. `≡N` glyph.
- **Branch count** — local branches (loose + packed).
- **Commits in the last 30 days** — recent activity gauge.
- **Activity tier** — `recent` (< 14d) → `active` (< stale_days) →
  `cold` (< 365d) → `dormant` (≥ 365d) → `empty` (no commits). The
  active/cold boundary tracks `stale_days` so the tier and the `▲`
  stale flag never contradict each other.
- **Stale flag** — `▲` glyph for repos older than `stale_days`.
- **Worktree count** — repos sharing a `CommonGitDir`. Linked
  worktrees of one project share project-identity but each gets its
  own row.

The `Highlights` helper combines these into a single human-readable
line that drives the table glyphs, the status-bar count, the detail
pane's "Highlights" line, and the `export` markdown flags — one
source of truth.

## Cache

`$XDG_CACHE_HOME/atlas/cache.json` (default
`~/.cache/atlas/cache.json` on every OS — atlas ignores
`os.UserCacheDir`'s macOS path of `~/Library/Caches`). Global,
keyed by absolute repo path.
The CLI/TUI filter by the active root at read time, so running atlas
from `~/projects/go` and from `~/projects` shares one cache and only
refreshes what's needed for the active subtree.

The cache is best-effort: missing/corrupt files just trigger a cold
rebuild. Schema version is bumped on incompatible changes; old caches
drop on load.

## Troubleshooting

- **atlas finds no repos under my root.** Check that the projects
  aren't deeper than `max_depth` (default 6) and that they're not
  matched by an entry in `skip_dirs`. Both are documented in the
  generated config — uncomment, edit, save, relaunch.
- **Colors look wrong or unreadable.** Set `theme = "ansi"` in
  `~/.config/atlas/config.toml`; the ANSI palette follows your
  terminal's colorscheme.
- **Cache feels stale.** Press `r` inside the TUI or run
  `atlas refresh` to force a fresh read of every repo under the
  active root.

## License

[MIT](LICENSE) © Seth Deckard
