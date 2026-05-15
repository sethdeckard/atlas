package tui

import "github.com/sethdeckard/atlas/internal/repo"

// Messages flow from tea.Cmds back into the model. The `gen` field on
// refresh messages lets the model drop superseded refreshes (e.g. user
// pressed `R` while an earlier refresh was still streaming).

type discoveredMsg struct {
	paths []string
	err   error
}

type refreshStartedMsg struct {
	gen   int
	ch    <-chan repo.Repo
	total int
}

type repoRefreshedMsg struct {
	gen  int
	repo repo.Repo
}

type refreshDoneMsg struct {
	gen int
}

type cacheSavedMsg struct {
	err error
}

type errMsg struct {
	err error
}

type clearStatusMsg struct{}

// initialLoadMsg is the kickoff for the warm-launch detail-pane load.
// Init can't directly call scheduleRecentCommitsLoad because Init's
// value-receiver makes any recentGen mutation invisible to the live
// model in tea.Program — the resulting tick would then be rejected as
// stale on arrival. Instead Init dispatches a cmd that emits this msg,
// which Update handles against the live model where mutations persist.
type initialLoadMsg struct{}

// recentCommitsTickMsg is the debounce wakeup. The actual fetch is
// dispatched only when the tick's gen still matches the model's
// current recentGen — fast scrolling supersedes earlier ticks.
type recentCommitsTickMsg struct {
	gen  int
	path string
}

// recentCommitsLoadedMsg carries the loaded subjects (or err). Cached
// unconditionally on the model when err is nil so re-selecting the same
// repo is instant.
type recentCommitsLoadedMsg struct {
	path  string
	lines []string
	err   error
}

// recentCommitsState is the explicit per-repo lifecycle for the M5
// detail-pane lazy-load: loading is "fetch in flight"; loaded is "we
// have an answer (which may be empty or carry an error)". Using a
// struct rather than an overloaded nil/empty []string removes the
// ambiguity between "not loaded yet" and "loaded with no commits" and
// gives the renderer a clean error case to show. The map key remaining
// absent still means "never requested", so missing-key + the two
// states give the three lifecycle phases the UI needs.
type recentCommitsState struct {
	loading bool
	loaded  bool
	lines   []string
	err     error
}
