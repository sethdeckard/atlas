# atlas demo tape — record with `vhs demo/atlas.tape` from the atlas repo
# root. Produces demo/atlas.gif.
#
# Prereqs:
#   - vhs installed (`brew install vhs`)
#   - atlas tree generated (`./demo/seed.sh`)
#   - Go toolchain on PATH — the tape builds atlas from source so the
#     recording always reflects the current working tree. No need to
#     `brew install` or `go install` atlas beforehand.
#
# This is a starter script: timing, captions, and the exact key sequence
# are meant to be iterated once you see the first render.

Output demo/atlas.gif

Set FontSize 14
Set Width 1280
Set Height 720
Set Shell "zsh"
Set TypingSpeed 60ms

# Build atlas from the current source into a throwaway dir and prepend
# it to PATH. Throwaway XDG dirs point at the generated demo tree so
# the user's real ~/.config/atlas/config.toml is untouched. macOS will
# GC the temp dirs automatically.
Hide
Type "ATLAS_BIN_DIR=$(mktemp -d)"
Enter
Type "go build -o $ATLAS_BIN_DIR/atlas ./cmd/atlas"
Enter
Sleep 5s
Type "export PATH=$ATLAS_BIN_DIR:$PATH"
Enter
Type "export XDG_CONFIG_HOME=$(mktemp -d)"
Enter
Type "export XDG_CACHE_HOME=$(mktemp -d)"
Enter
Type "mkdir -p $XDG_CONFIG_HOME/atlas"
Enter
Type `echo "root = \"$(pwd)/../atlas.demo-projects\"" > $XDG_CONFIG_HOME/atlas/config.toml`
Enter
Type "clear"
Enter
Show

# Initial render: warm-launch table populated immediately from cache
# after the first refresh. Pause so viewers can read the glyphs.
Type "atlas"
Enter
Sleep 3s

# Filter — only go repos visible.
Type "/go"
Sleep 2s
Escape
Sleep 1s

# Cycle sort key (last_commit_at → repo).
Type "s"
Sleep 1500ms
# And back.
Type "s"
Sleep 1500ms

# Cycle grouping: activity → top_dir → language → worktree → none.
Tab
Sleep 1500ms
Tab
Sleep 1500ms

# Worktree view — the headline. Each multi-worktree project is a tree:
# the primary checkout anchors its linked worktrees (├─ / └─), a
# forgotten one carries ▲ / ⊘, and the anchor rolls a ⊘ up. Pause,
# then walk the selection so the detail-pane roster updates.
Tab
Sleep 3s
Type "j"
Sleep 800ms
Type "j"
Sleep 800ms
Type "j"
Sleep 1500ms
Type "k"
Sleep 1500ms

# tab → none, then wrap back to the activity default.
Tab
Sleep 1500ms
Tab
Sleep 1500ms

# Help overlay.
Type "?"
Sleep 3s
Escape
Sleep 1s

# Exit cleanly (saves the cache).
Type "q"
Sleep 1s
