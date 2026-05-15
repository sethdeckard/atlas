# atlas demo

Scaffolding for recording the atlas demo tape.

- `seed.sh` generates a curated fake project tree at
  `../atlas.demo-projects/` (a sibling of the atlas repo) that
  exercises every signal atlas surfaces: language detection across
  six top-dirs, all five activity tiers, dirty / stash / ahead /
  behind / branch-count / worktree-set glyphs, and one empty repo.
- `atlas.tape` is a [vhs](https://github.com/charmbracelet/vhs)
  script that records a TUI session against that tree and produces
  `atlas.gif`.

The demo tree lives outside the atlas repo so tear-down is a single
`rm -rf` (no `.gitignore` entries, no risk of accidentally
committing fake repos).

## Run it

```sh
# one-time
brew install vhs

# preview what the seed would create — no filesystem changes
./demo/seed.sh --dry-run

# create the tree (wipes and regenerates on every run)
./demo/seed.sh

# record
vhs demo/atlas.tape

# tear down (preview first if you like)
./demo/seed.sh --teardown --dry-run
./demo/seed.sh --teardown
```

The tape builds `atlas` from source into a throwaway dir at the
start of recording and prepends it to `PATH`, so the GIF always
reflects the current working tree — no need to `brew install` or
`go install` atlas first. It also uses throwaway `XDG_CONFIG_HOME`
/ `XDG_CACHE_HOME` so your real `~/.config/atlas/config.toml` and
`~/.cache/atlas/` are not touched. macOS garbage-collects the
temp dirs automatically.

## Customizing

- The repo inventory lives in the bottom half of `seed.sh` — one
  `create_repo` / `add_worktree` line per entry. Supported flags
  per `create_repo`:
  - `dirty` — uncommitted change in the worktree.
  - `stash:N` — push N entries onto the stash.
  - `branches:N` — add N additional local branches.
  - `ahead:N`, `behind:N` — synthesize an upstream so atlas
    computes `↑N` / `↓N` against `refs/remotes/origin/main`. The
    fake remote URL is `https://example.com/atlas-demo/<name>.git`
    (RFC 2606 reserved domain — never resolves anywhere real).
  - `polyglot` — adds a `Dockerfile` alongside the primary
    manifest, so atlas detects `docker` as an additional language.
  - `empty` — no commits (lands in the `empty` activity tier).
- Activity tier ages are set inside `age_for_tier` (recent → 3
  days, active → 30, cold → 180, dormant → 500). Adjust if you
  want to demo a non-default `stale_days`.
- `atlas.tape` is a starter — pacing, captions, and exact key
  sequence are meant to be iterated once you see the first render.
  vhs docs: <https://github.com/charmbracelet/vhs>
