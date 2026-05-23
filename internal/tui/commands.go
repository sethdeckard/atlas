package tui

import (
	"context"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/git"
	"github.com/sethdeckard/atlas/internal/repo"
	"github.com/sethdeckard/atlas/internal/scan"

	tea "github.com/charmbracelet/bubbletea"
)

// discoverCmd walks the filesystem and reports the discovered paths.
// ctx is the program-level context — cancellation (e.g. from a SIGINT
// caught by signal.NotifyContext or from program shutdown) terminates the
// walk early instead of finishing a multi-thousand-dir scan. forceFull is
// echoed onto discoveredMsg so handleDiscovered knows whether to force a
// full re-read (the r refresh) or run the incremental launch path.
func discoverCmd(ctx context.Context, root string, opts scan.Options, forceFull bool) tea.Cmd {
	return func() tea.Msg {
		paths, err := scan.Discover(ctx, root, opts)
		return discoveredMsg{paths: paths, err: err, forceFull: forceFull}
	}
}

// startRefreshCmd kicks off a worker pool reading the given paths and emits
// refreshStartedMsg with the result channel + generation id. The model
// tracks gen so out-of-date refresh messages can be dropped if the user
// triggers a fresh `R` mid-stream.
func startRefreshCmd(ctx context.Context, gen int, paths []string) tea.Cmd {
	return func() tea.Msg {
		ch := cache.Refresh(ctx, paths, repo.Read, 8)
		return refreshStartedMsg{gen: gen, ch: ch, total: len(paths)}
	}
}

// nextRefreshCmd reads the next value from the active refresh channel.
// Returns repoRefreshedMsg per value and refreshDoneMsg when the channel
// closes. The model re-issues this command after every repoRefreshedMsg.
func nextRefreshCmd(ch <-chan repo.Repo, gen int) tea.Cmd {
	return func() tea.Msg {
		r, ok := <-ch
		if !ok {
			return refreshDoneMsg{gen: gen}
		}
		return repoRefreshedMsg{gen: gen, repo: r}
	}
}

// startStatusRefreshCmd is the same shape as startRefreshCmd but uses
// cache.RefreshStatus (lightweight, status-only update). Used to catch
// dirty/untracked changes that the mtime fingerprints miss without paying
// for a full re-read.
func startStatusRefreshCmd(ctx context.Context, gen int, cached []repo.Repo, updater cache.StatusUpdater) tea.Cmd {
	return func() tea.Msg {
		ch := cache.RefreshStatus(ctx, cached, updater, 8)
		return refreshStartedMsg{gen: gen, ch: ch, total: len(cached)}
	}
}

// saveCacheCmd persists the cache atomically. Errors are surfaced via
// cacheSavedMsg so the model can show a transient warning.
func saveCacheCmd(cachePath string, c *cache.Cache) tea.Cmd {
	return func() tea.Msg {
		return cacheSavedMsg{err: cache.Save(cachePath, c)}
	}
}

// clearStatusAfter returns a tea.Cmd that fires clearStatusMsg after d.
func clearStatusAfter(d time.Duration) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return clearStatusMsg{}
	})
}

// recentCommitsTickCmd debounces the recent-commits load. The TUI
// schedules a tick whenever the selected repo changes; the resulting
// message carries the gen counter so the model can drop superseded
// ticks (fast scrolling) without spawning a shellout per row.
func recentCommitsTickCmd(d time.Duration, gen int, path string) tea.Cmd {
	return tea.Tick(d, func(time.Time) tea.Msg {
		return recentCommitsTickMsg{gen: gen, path: path}
	})
}

// fetchRecentCommitsCmd shells out to git for the given repo's last
// recentCommitsCount subjects.
func fetchRecentCommitsCmd(ctx context.Context, path string) tea.Cmd {
	return func() tea.Msg {
		paths, err := git.ResolvePaths(path)
		if err != nil {
			return recentCommitsLoadedMsg{path: path, err: err}
		}
		lines, err := git.RecentCommits(ctx, paths, recentCommitsCount)
		return recentCommitsLoadedMsg{path: path, lines: lines, err: err}
	}
}

// recentCommitsCount is how many subjects the detail pane shows.
const recentCommitsCount = 5
