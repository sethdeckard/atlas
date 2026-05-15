#!/usr/bin/env bash
# demo/seed.sh — generate a fake project tree for the atlas demo tape.
#
# Creates a sibling directory `../atlas.demo-projects` (relative to the atlas
# repo root) populated with a curated set of git repositories that
# exercise every signal atlas surfaces: language detection, activity
# tiers, dirty state, stash count, branch count, ahead/behind upstream,
# linked worktrees, and an empty repo.
#
# Usage:
#   ./demo/seed.sh             # wipe and regenerate
#   ./demo/seed.sh --dry-run   # print what would be created; touch nothing
#
# Tear-down is a single `rm -rf ../atlas.demo-projects`. No global git config
# is read or written; each generated repo has local user.name/user.email.

set -euo pipefail

# Isolate every `git` invocation below from the user's global / system
# config. Without this, a global `core.hooksPath` pre-commit hook, GPG
# signing requirement, or `core.templatesDir` installing hooks into
# freshly-init'd repos would intercept and could abort the seed. Same
# pattern used by internal/gitfixture/fixture.go.
export GIT_CONFIG_GLOBAL=/dev/null
export GIT_CONFIG_NOSYSTEM=1
export GIT_TERMINAL_PROMPT=0

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
DEMO_ROOT="$(cd "$REPO_ROOT/.." && pwd)/atlas.demo-projects"

DRY_RUN=0
TEARDOWN=0

usage() {
    cat <<EOF
Usage: $0 [--dry-run] [--teardown]

  --dry-run    Print what would happen without touching the filesystem.
               Combined with --teardown, previews the removal.
  --teardown   Remove the demo tree at \$DEMO_ROOT and exit.
  -h, --help   Show this message.
EOF
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --dry-run)  DRY_RUN=1; shift ;;
        --teardown) TEARDOWN=1; shift ;;
        -h|--help)  usage; exit 0 ;;
        *)          echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
    esac
done

if [[ $TEARDOWN -eq 1 ]]; then
    if [[ ! -d "$DEMO_ROOT" ]]; then
        echo "nothing to tear down — $DEMO_ROOT does not exist"
        exit 0
    fi
    repo_count=$(find "$DEMO_ROOT" -maxdepth 4 -name .git 2>/dev/null | wc -l | tr -d ' ')
    if [[ $DRY_RUN -eq 1 ]]; then
        echo "DRY RUN — would remove $DEMO_ROOT (contains $repo_count repo(s))"
    else
        echo "removing $DEMO_ROOT (contains $repo_count repo(s))"
        rm -rf "$DEMO_ROOT"
        echo "done"
    fi
    exit 0
fi

# Cross-platform "N days ago" → ISO8601 UTC. macOS ships BSD date (-r);
# Linux ships GNU date (-d). Try BSD first, fall back to GNU.
epoch_days_ago() {
    local days=$1 now then
    now=$(date +%s)
    then=$((now - days * 86400))
    if date -r "$then" -u +%Y-%m-%dT%H:%M:%SZ 2>/dev/null; then
        return
    fi
    date -u -d "@$then" +%Y-%m-%dT%H:%M:%SZ
}

# Tier ages picked to land safely on either side of the configurable
# stale_days boundary (60 or 90 by default). See internal/repo/activity.go.
age_for_tier() {
    case "$1" in
        recent)  echo 3 ;;
        active)  echo 30 ;;
        cold)    echo 180 ;;
        dormant) echo 500 ;;
        empty)   echo 0 ;;
        *)       echo "unknown tier: $1" >&2; return 2 ;;
    esac
}

manifest_filename() {
    case "$1" in
        go)     echo go.mod ;;
        rust)   echo Cargo.toml ;;
        node)   echo package.json ;;
        python) echo pyproject.toml ;;
        ruby)   echo Gemfile ;;
        java)   echo pom.xml ;;
        misc)   echo "" ;;
        *)      echo "unknown lang: $1" >&2; return 2 ;;
    esac
}

manifest_content() {
    local lang=$1 name=$2
    case "$lang" in
        go)     printf 'module demo/%s\n\ngo 1.23\n' "$name" ;;
        rust)   printf '[package]\nname = "%s"\nversion = "0.1.0"\nedition = "2021"\n' "$name" ;;
        node)   printf '{\n  "name": "%s",\n  "version": "0.1.0"\n}\n' "$name" ;;
        python) printf '[project]\nname = "%s"\nversion = "0.1.0"\n' "$name" ;;
        ruby)   printf 'source "https://rubygems.org"\n' ;;
        java)   printf '<project>\n  <groupId>demo</groupId>\n  <artifactId>%s</artifactId>\n  <version>0.1.0</version>\n</project>\n' "$name" ;;
        misc)   ;;
        *)      echo "unknown lang: $lang" >&2; return 2 ;;
    esac
}

COUNT_TOTAL=0
COUNT_RECENT=0
COUNT_ACTIVE=0
COUNT_COLD=0
COUNT_DORMANT=0
COUNT_EMPTY=0

bump_tier_count() {
    case "$1" in
        recent)  COUNT_RECENT=$((COUNT_RECENT + 1)) ;;
        active)  COUNT_ACTIVE=$((COUNT_ACTIVE + 1)) ;;
        cold)    COUNT_COLD=$((COUNT_COLD + 1)) ;;
        dormant) COUNT_DORMANT=$((COUNT_DORMANT + 1)) ;;
        empty)   COUNT_EMPTY=$((COUNT_EMPTY + 1)) ;;
    esac
    COUNT_TOTAL=$((COUNT_TOTAL + 1))
}

# create_repo lang name tier [flag...]
# Flags: dirty | stash:N | branches:N | ahead:N | behind:N | polyglot
create_repo() {
    local lang=$1 name=$2 tier=$3
    shift 3
    local flags=("$@")

    bump_tier_count "$tier"

    if [[ $DRY_RUN -eq 1 ]]; then
        local flag_str="(none)"
        if [[ ${#flags[@]} -gt 0 ]]; then
            flag_str="${flags[*]}"
        fi
        printf '  would create %-8s/%-18s tier=%-8s flags=%s\n' "$lang" "$name" "$tier" "$flag_str"
        return
    fi

    local dir="$DEMO_ROOT/$lang/$name"
    mkdir -p "$dir"

    git -C "$dir" init -q --initial-branch=main
    git -C "$dir" config user.email "demo@atlas.local"
    git -C "$dir" config user.name "Atlas Demo"
    git -C "$dir" config commit.gpgsign false

    # Manifest (skipped for lang=none) + README so the detail-pane has
    # something readable to render.
    local manifest
    manifest=$(manifest_filename "$lang")
    if [[ -n "$manifest" ]]; then
        manifest_content "$lang" "$name" > "$dir/$manifest"
    fi
    printf '# %s\n\nDemo project for the atlas tape.\n' "$name" > "$dir/README.md"

    # Polyglot: Dockerfile beside the primary manifest. atlas reads both
    # and renders the primary language with `docker` appended.
    local f
    for f in "${flags[@]+"${flags[@]}"}"; do
        if [[ "$f" == "polyglot" ]]; then
            printf 'FROM scratch\n' > "$dir/Dockerfile"
        fi
    done

    # Empty tier skips the commit entirely (atlas's "empty" classification).
    if [[ "$tier" == "empty" ]]; then
        return
    fi

    local age commit_date
    age=$(age_for_tier "$tier")
    commit_date=$(epoch_days_ago "$age")
    git -C "$dir" add -A
    GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" \
        git -C "$dir" commit -q -m "Initial commit"

    # `dirty` must be applied LAST: stash:* runs `git stash push` which
    # would otherwise stash the uncommitted "modified" line and leave the
    # worktree clean. Defer dirty until every other flag has run.
    local want_dirty=0

    for f in "${flags[@]+"${flags[@]}"}"; do
        case "$f" in
            polyglot) ;; # handled pre-commit
            dirty)
                want_dirty=1
                ;;
            stash:*)
                local n=${f#stash:} i
                for ((i = 0; i < n; i++)); do
                    printf 'stash content %d\n' "$i" >> "$dir/README.md"
                    git -C "$dir" stash push -q -m "stashed change $((i + 1))"
                done
                ;;
            branches:*)
                local n=${f#branches:} i
                for ((i = 0; i < n; i++)); do
                    git -C "$dir" branch "feature-$((i + 1))"
                done
                ;;
            ahead:*)
                local n=${f#ahead:} i upstream_sha
                # Synthetic origin so `git rev-parse @{u}` resolves. atlas
                # never fetches, so the URL is purely cosmetic — but a
                # fully-configured remote (with fetch refspec) is required
                # for the tracking ref to be recognized as an upstream.
                git -C "$dir" remote add origin "https://example.com/atlas-demo/$name.git"
                upstream_sha=$(git -C "$dir" rev-parse HEAD)
                for ((i = 0; i < n; i++)); do
                    printf 'ahead %d\n' "$i" >> "$dir/README.md"
                    git -C "$dir" add README.md
                    GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" \
                        git -C "$dir" commit -q -m "ahead commit $((i + 1))"
                done
                git -C "$dir" update-ref refs/remotes/origin/main "$upstream_sha"
                git -C "$dir" config branch.main.remote origin
                git -C "$dir" config branch.main.merge refs/heads/main
                ;;
            behind:*)
                local n=${f#behind:} i remote_sha
                git -C "$dir" remote add origin "https://example.com/atlas-demo/$name.git"
                for ((i = 0; i < n; i++)); do
                    printf 'remote %d\n' "$i" >> "$dir/README.md"
                    git -C "$dir" add README.md
                    GIT_AUTHOR_DATE="$commit_date" GIT_COMMITTER_DATE="$commit_date" \
                        git -C "$dir" commit -q -m "behind commit $((i + 1))"
                done
                remote_sha=$(git -C "$dir" rev-parse HEAD)
                git -C "$dir" reset -q --hard "HEAD~$n"
                git -C "$dir" update-ref refs/remotes/origin/main "$remote_sha"
                git -C "$dir" config branch.main.remote origin
                git -C "$dir" config branch.main.merge refs/heads/main
                ;;
            *)
                echo "unknown flag for $name: $f" >&2; return 2 ;;
        esac
    done

    if [[ $want_dirty -eq 1 ]]; then
        printf 'modified\n' >> "$dir/README.md"
    fi
}

# add_worktree parent_lang parent_name worktree_name
# Linked worktree at $DEMO_ROOT/$parent_lang/$worktree_name on a new
# branch named after the worktree. Bumps the active-tier count since
# worktrees inherit recency from HEAD which we leave at parent's tip.
add_worktree() {
    local parent_lang=$1 parent_name=$2 worktree_name=$3

    bump_tier_count active

    if [[ $DRY_RUN -eq 1 ]]; then
        printf '  would create %-8s/%-18s tier=%-8s flags=worktree-of:%s\n' \
            "$parent_lang" "$worktree_name" "active" "$parent_name"
        return
    fi

    local parent_dir="$DEMO_ROOT/$parent_lang/$parent_name"
    local worktree_dir="$DEMO_ROOT/$parent_lang/$worktree_name"
    git -C "$parent_dir" worktree add -q -b "$worktree_name" "$worktree_dir"
}

# -----------------------------------------------------------------------
# Orchestration.

if [[ $DRY_RUN -eq 1 ]]; then
    if [[ -d "$DEMO_ROOT" ]]; then
        echo "DRY RUN — would wipe and recreate $DEMO_ROOT"
    else
        echo "DRY RUN — would create $DEMO_ROOT"
    fi
    echo
else
    if [[ -d "$DEMO_ROOT" ]]; then
        echo "removing existing $DEMO_ROOT"
        rm -rf "$DEMO_ROOT"
    fi
    mkdir -p "$DEMO_ROOT"
    echo "seeding $DEMO_ROOT"
    echo
fi

# Inventory — keep aligned with the table in the plan and the README.
create_repo go     atria          active  dirty stash:1 ahead:2
create_repo go     atlas          recent  polyglot
create_repo go     loadout        active  branches:3
create_repo go     solopub        cold
create_repo go     colorpreview   dormant
create_repo rust   ferro          active  behind:3
create_repo rust   blade          recent  dirty
create_repo rust   tessera        cold    stash:2
create_repo node   spectrum       active  dirty branches:2
create_repo node   astrolabe      recent
create_repo node   quartz         dormant
create_repo python hypothesis-lab active  ahead:5
create_repo python datafold       recent  polyglot
create_repo python sundial        cold
create_repo ruby   aether         active  dirty
create_repo ruby   midas          recent  stash:1
create_repo java   beacon         cold    branches:4
create_repo java   lighthouse     active  ahead:1
create_repo misc   scratch        empty

add_worktree go   atria  atria-feat-foo
add_worktree go   atria  atria-feat-bar
add_worktree ruby aether aether-hotfix

echo
if [[ $DRY_RUN -eq 1 ]]; then
    echo "DRY RUN summary ($COUNT_TOTAL repo(s)):"
else
    echo "seeded $COUNT_TOTAL repo(s):"
fi
printf '  recent:  %d\n' "$COUNT_RECENT"
printf '  active:  %d\n' "$COUNT_ACTIVE"
printf '  cold:    %d\n' "$COUNT_COLD"
printf '  dormant: %d\n' "$COUNT_DORMANT"
printf '  empty:   %d\n' "$COUNT_EMPTY"
echo

if [[ $DRY_RUN -eq 1 ]]; then
    echo "(no filesystem changes made)"
else
    echo "demo tree at: $DEMO_ROOT"
    echo "next: point atlas's root at that path, then \`vhs demo/atlas.tape\`"
fi
