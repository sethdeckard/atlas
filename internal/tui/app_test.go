package tui

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/config"
	"github.com/sethdeckard/atlas/internal/repo"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

func init() {
	// Pin the clock so relative-time formatting is deterministic.
	SetNowFunc(func() time.Time {
		return time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	})
}

func newTestModel(t *testing.T, repos []repo.Repo, root string) Model {
	t.Helper()
	c := cache.New()
	for _, r := range repos {
		c.Repos[r.Path] = r
	}
	cachePath := t.TempDir() + "/cache.json"
	cfg := config.Defaults()
	return New(context.Background(), c, cachePath, cfg, root)
}

func sampleRepo(name, path, branch string, lastCommitDaysAgo int, dirty bool) repo.Repo {
	t := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Add(time.Duration(-lastCommitDaysAgo*24) * time.Hour)
	relPath := path
	if i := strings.Index(path, "/"); i >= 0 {
		relPath = "~" + path[i:]
	}
	return repo.Repo{
		Name:         name,
		Path:         path,
		RelPath:      relPath,
		Kind:         repo.KindRepo,
		Branch:       branch,
		LastCommitAt: &t,
		Dirty:        dirty,
		CommonGitDir: path + "/.git",
	}
}

func TestView_EmptyState(t *testing.T) {
	m := newTestModel(t, nil, "/home/u/projects")
	m.width = 80
	m.height = 20
	// Force-clear scanning so the post-discover empty-state copy renders
	// instead of the "Discovering..." cold-cache message — New sets
	// scanning=true whenever rebuildRepos yields zero rows.
	m.scanning = false
	out := m.View()
	if !strings.Contains(out, "No repositories found") {
		t.Errorf("expected empty-state hint; got:\n%s", out)
	}
	if !strings.Contains(out, "max_depth") {
		t.Errorf("expected empty-state hint to mention max_depth; got:\n%s", out)
	}
	if !strings.Contains(out, "0 repos") {
		t.Errorf("expected status bar to show 0 repos; got:\n%s", out)
	}
}

func TestView_ScanningState(t *testing.T) {
	m := newTestModel(t, nil, "/home/u/projects")
	m.width = 80
	m.height = 20
	// New already set scanning=true for an empty cache, but assert it
	// explicitly so the test stays correct if New's contract changes.
	m.scanning = true
	out := m.View()
	if !strings.Contains(out, "Discovering repositories under") {
		t.Errorf("expected discovering message; got:\n%s", out)
	}
	if strings.Contains(out, "No repositories found") {
		t.Errorf("scanning state should not render empty-state hint; got:\n%s", out)
	}
}

// TestScanning_PersistsThroughDiscoveryUntilFirstRefresh guards the
// cold-launch flow: when discovery finds paths but the cache is empty,
// scanning must remain true until the first repoRefreshedMsg arrives,
// so the View doesn't briefly flash the empty-state hint while reads
// are still in flight.
func TestScanning_PersistsThroughDiscoveryUntilFirstRefresh(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.width = 80
	m.height = 20
	if !m.scanning {
		t.Fatalf("New on empty cache should set scanning=true")
	}

	// Discovery completes with paths still to read.
	updated, _ := m.Update(discoveredMsg{paths: []string{"/r/alpha", "/r/beta"}})
	m = updated.(Model)
	if !m.scanning {
		t.Fatalf("scanning must stay true while refresh is in flight; cleared too early")
	}
	if !strings.Contains(m.View(), "Discovering repositories under") {
		t.Errorf("View should still show discovering copy; got:\n%s", m.View())
	}

	// First refreshed row arrives — scanning clears, table renders.
	upd := sampleRepo("alpha", "/r/alpha", "main", 1, false)
	updated, _ = m.Update(repoRefreshedMsg{gen: m.refreshGen, repo: upd})
	m = updated.(Model)
	if m.scanning {
		t.Fatalf("first repoRefreshedMsg should clear scanning")
	}
}

// TestScanning_ClearsOnEmptyDiscovery covers the no-repos-at-all case:
// discovery returns zero paths, no refresh follows, and the empty-state
// hint must surface instead of "Discovering..." sticking forever.
func TestScanning_ClearsOnEmptyDiscovery(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.width = 80
	m.height = 20

	updated, _ := m.Update(discoveredMsg{paths: nil})
	m = updated.(Model)
	if m.scanning {
		t.Fatalf("empty discovery should clear scanning")
	}
	if !strings.Contains(m.View(), "No repositories found") {
		t.Errorf("View should show empty-state hint after empty discovery; got:\n%s", m.View())
	}
}

// TestScanning_ClearsOnRefreshDone covers the edge where refresh
// completes without ever producing a row (every read errored out, or
// all paths went gone-from-disk between discover and read). Without
// this clear in advanceAfterPhase the View would stay on
// "Discovering..." indefinitely.
func TestScanning_ClearsOnRefreshDone(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.width = 80
	m.height = 20
	m.scanning = true
	m.refreshing = true
	m.refreshGen = 1
	m.pendingStatusPass = nil

	updated, _ := m.Update(refreshDoneMsg{gen: 1})
	m = updated.(Model)
	if m.scanning {
		t.Fatalf("advanceAfterPhase should clear scanning when refresh ends")
	}
}

func TestView_WarmCacheRendersRows(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/home/u/projects/alpha", "main", 1, false),
		sampleRepo("beta", "/home/u/projects/beta", "main", 2, true),
		sampleRepo("gamma", "/home/u/projects/gamma", "trunk", 30, false),
	}
	m := newTestModel(t, repos, "/home/u/projects")
	m.width = 80
	m.height = 20
	out := m.View()
	for _, name := range []string{"alpha", "beta", "gamma"} {
		if !strings.Contains(out, name) {
			t.Errorf("expected %q in view; got:\n%s", name, out)
		}
	}
	if !strings.Contains(out, "3 repos") {
		t.Errorf("status bar should show 3 repos; got:\n%s", out)
	}
	if !strings.Contains(out, "1 dirty") {
		t.Errorf("status bar should show 1 dirty; got:\n%s", out)
	}
}

func TestUpdate_NavigationKeys(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 0, false),
		sampleRepo("beta", "/r/beta", "main", 1, false),
		sampleRepo("gamma", "/r/gamma", "main", 2, false),
		sampleRepo("delta", "/r/delta", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Down 3 times.
	for i := 0; i < 3; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	if m.selected != 3 {
		t.Errorf("after 3 downs: selected = %d; want 3", m.selected)
	}

	// Down again — clamped at last index.
	updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = updated.(Model)
	if m.selected != 3 {
		t.Errorf("Down past end should clamp; got %d", m.selected)
	}

	// Up once.
	updated, _ = m.Update(tea.KeyMsg{Type: tea.KeyUp})
	m = updated.(Model)
	if m.selected != 2 {
		t.Errorf("after 1 up: selected = %d; want 2", m.selected)
	}
}

func TestUpdate_RepoRefreshedUpdatesCache(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Simulate a refresh in flight, with a fake stream so continueRefresh
	// returns a non-nil tea.Cmd.
	stream := make(chan repo.Repo)
	m.refreshGen = 7
	m.refreshing = true
	m.refreshTotal = 1
	m.refreshDoneCount = 0
	m.activeCh = stream

	updated := sampleRepo("alpha", "/r/alpha", "main", 0, true)
	out, cmd := m.Update(repoRefreshedMsg{gen: 7, repo: updated})
	mm := out.(Model)
	if got := mm.cache.Repos["/r/alpha"].Dirty; !got {
		t.Errorf("expected cache entry to be marked dirty after refresh")
	}
	if mm.refreshDoneCount != 1 {
		t.Errorf("refreshDoneCount = %d; want 1", mm.refreshDoneCount)
	}
	if cmd == nil {
		t.Errorf("expected a continuation command (next refresh read)")
	}
	close(stream)
}

func TestUpdate_RepoRefreshedDropsStaleGen(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.refreshGen = 9 // current gen
	updated := sampleRepo("alpha", "/r/alpha", "main", 0, true)
	out, _ := m.Update(repoRefreshedMsg{gen: 3, repo: updated}) // older gen
	mm := out.(Model)
	if got := mm.cache.Repos["/r/alpha"].Dirty; got {
		t.Errorf("stale-gen refresh should be ignored; cache mutated")
	}
}

func TestStatusBar_RefreshIndicator(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("a", "/r/a", "main", 1, false),
		sampleRepo("b", "/r/b", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 200 // wide enough that the status bar doesn't wrap mid-token
	m.height = 20
	m.refreshing = true
	m.refreshTotal = 2
	m.refreshDoneCount = 1
	out := m.View()
	if !strings.Contains(out, "[refreshing 1/2]") {
		t.Errorf("expected refresh indicator [refreshing 1/2]; got:\n%s", out)
	}
}

// TestSelectedRowStaysInViewport guards the scroll-offset logic: navigating
// past the bottom of the visible body must shift scrollOffset so the
// selected row is still rendered.
func TestSelectedRowStaysInViewport(t *testing.T) {
	repos := make([]repo.Repo, 30)
	for i := range repos {
		repos[i] = sampleRepo(
			fmt.Sprintf("repo%02d", i),
			fmt.Sprintf("/r/repo%02d", i),
			"main",
			i,
			false,
		)
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 8 // small body: status (1) + hint (1) + header (1) + ~5 rows

	// Down 20 times — selection should now be far below the initial
	// viewport.
	for i := 0; i < 20; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	if m.selected != 20 {
		t.Fatalf("expected selected=20; got %d", m.selected)
	}
	rows := m.viewportRows()
	if m.scrollOffset > m.selected || m.selected >= m.scrollOffset+rows {
		t.Errorf("selected=%d outside viewport [%d, %d)", m.selected, m.scrollOffset, m.scrollOffset+rows)
	}

	// Selected name must appear in the rendered View.
	out := m.View()
	wantName := repos[20].Name
	if !strings.Contains(out, wantName) {
		t.Errorf("expected %q in rendered view; got:\n%s", wantName, out)
	}
}

// TestDiscoveryClampsScrollOffset guards against a bug where discovery
// shrinks the repo list (cache entries deleted as gone) but scrollOffset
// still points past the new end, leaving the body empty.
func TestDiscoveryClampsScrollOffset(t *testing.T) {
	repos := make([]repo.Repo, 30)
	paths := make([]string, len(repos))
	for i := range repos {
		repos[i] = sampleRepo(
			fmt.Sprintf("repo%02d", i),
			fmt.Sprintf("/r/repo%02d", i),
			"main",
			i,
			false,
		)
		paths[i] = repos[i].Path
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 8

	// Scroll way down.
	for i := 0; i < 25; i++ {
		updated, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
		m = updated.(Model)
	}
	if m.scrollOffset == 0 {
		t.Fatalf("expected non-zero scrollOffset after scrolling; got 0")
	}

	// Discovery returns only the first 5 paths — the other 25 are gone.
	updated, _ := m.Update(discoveredMsg{paths: paths[:5]})
	m = updated.(Model)

	// Selected is now clamped to the new last index (4); scrollOffset must
	// be inside the new bounds.
	if m.selected >= len(m.repos) {
		t.Errorf("selected=%d not clamped to repos len=%d", m.selected, len(m.repos))
	}
	rows := m.viewportRows()
	if m.scrollOffset+rows < m.selected || m.scrollOffset > m.selected {
		t.Errorf("scrollOffset=%d not clamped: selected=%d rows=%d", m.scrollOffset, m.selected, rows)
	}
	out := m.View()
	if !strings.Contains(out, m.repos[m.selected].Name) {
		t.Errorf("expected selected repo %q in view; got:\n%s", m.repos[m.selected].Name, out)
	}
}

// TestSavesSerializeViaPendingQueue guards the out-of-order-save fix: while
// one save is in flight, additional save requests park their snapshot on
// pendingSave (overwriting any older queued snapshot). When the in-flight
// save completes, the pending one dispatches.
func TestSavesSerializeViaPendingQueue(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Trigger first save: refreshGen=0 with 10 prior refreshes pending.
	stream := make(chan repo.Repo, 1)
	m.refreshGen = 1
	m.refreshing = true
	m.refreshTotal = 30
	m.refreshDoneCount = 9 // next refresh tips us to 10 → triggers save
	m.refreshesSinceSave = 9
	m.activeCh = stream
	upd := sampleRepo("alpha", "/r/alpha", "main", 0, true)

	out, cmd := m.Update(repoRefreshedMsg{gen: 1, repo: upd})
	mm := out.(Model)
	if !mm.saves.InFlight() {
		t.Fatalf("first save should be in flight after every-N trigger")
	}
	if cmd == nil {
		t.Fatalf("expected save+continue cmds")
	}

	// Simulate refreshes #11-#19 arriving while save is still in flight.
	// None of these should dispatch a new save (saves.InFlight() is true).
	for i := 0; i < 9; i++ {
		mm.refreshesSinceSave = saveEveryNRefreshes // ready to fire each iteration
		out, _ = mm.Update(repoRefreshedMsg{gen: 1, repo: upd})
		mm = out.(Model)
	}
	if !mm.saves.InFlight() {
		t.Errorf("save should still be in flight; got InFlight=%v", mm.saves.InFlight())
	}
	if mm.saves.pending == nil {
		t.Errorf("expected a queued pending snapshot from suppressed every-N triggers")
	}

	// First save completes — pending should dispatch.
	out, cmd = mm.Update(cacheSavedMsg{err: nil})
	mm = out.(Model)
	if mm.saves.pending != nil {
		t.Errorf("pending should be cleared after dispatch; got %+v", mm.saves.pending)
	}
	if !mm.saves.InFlight() {
		t.Errorf("a new save should be in flight (the pending one)")
	}
	if cmd == nil {
		t.Errorf("expected a saveCacheCmd to be issued")
	}
	close(stream)
}

// TestQuitDefersUntilSavesDrain guards that pressing q while a save is in
// flight queues the latest snapshot and waits for the queue to drain
// before firing tea.Quit.
func TestQuitDefersUntilSavesDrain(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	// Set up: a save is currently in flight.
	m.saves.inFlight = true

	// User presses q.
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	mm := out.(Model)
	if !mm.quitPending {
		t.Fatalf("expected quitPending=true; got %v", mm.quitPending)
	}
	if mm.saves.pending == nil {
		t.Fatalf("expected pending snapshot to be queued")
	}
	if cmd != nil {
		t.Fatalf("expected nil cmd while waiting for in-flight save; got non-nil")
	}

	// In-flight save completes. The queued pending save should dispatch
	// (saves.InFlight() stays true), so we don't quit yet.
	out, _ = mm.Update(cacheSavedMsg{err: nil})
	mm = out.(Model)
	if !mm.saves.InFlight() {
		t.Errorf("expected pending save to dispatch and saves.InFlight() stays true")
	}

	// Last save completes. Now we should fire tea.Quit.
	out, cmd = mm.Update(cacheSavedMsg{err: nil})
	mm = out.(Model)
	if cmd == nil {
		t.Fatalf("expected tea.Quit cmd after queue drains")
	}
	// tea.Quit is a function value; calling it returns tea.QuitMsg.
	if msg := cmd(); msg == nil {
		t.Errorf("expected non-nil quit message")
	}
}

// TestBeginRefreshPhaseCancelsPrior guards the worker-leak fix: starting a
// new refresh phase must invoke the prior phase's cancel function so old
// workers blocked on `out <- result` get unblocked.
func TestBeginRefreshPhaseCancelsPrior(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	cancelled := false
	m.refreshCancel = func() { cancelled = true }

	updated, ctx := m.beginRefreshPhase(5)
	if !cancelled {
		t.Error("expected prior refreshCancel to be called when starting a new phase")
	}
	if ctx == nil {
		t.Error("expected non-nil ctx for the new phase")
	}
	if updated.refreshCancel == nil {
		t.Error("expected refreshCancel to be set for the new phase")
	}
}

func TestAdvanceAfterPhaseClearsRefreshCancel(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	called := false
	m.refreshCancel = func() { called = true }

	updated, _ := m.advanceAfterPhase()
	mm := updated.(Model)
	if !called {
		t.Error("expected refreshCancel called when phase chain ends")
	}
	if mm.refreshCancel != nil {
		t.Error("expected refreshCancel cleared after final phase")
	}
}

// TestSnapshotIsolatesAsyncSave guards the P1 race fix: mutating the live
// cache after Snapshot must not affect the snapshotted copy that the async
// save is iterating.
func TestSnapshotIsolatesAsyncSave(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")

	snap := m.cache.Snapshot()
	// Mutate live cache — must not affect snapshot.
	m.cache.Repos["/r/beta"] = sampleRepo("beta", "/r/beta", "main", 0, true)
	if _, exists := snap.Repos["/r/beta"]; exists {
		t.Errorf("snapshot mutated by live cache write")
	}
	if got := snap.Repos["/r/alpha"]; got.Name != "alpha" {
		t.Errorf("snapshot lost original entry: %+v", got)
	}
}

// TestHandleEnter_EmptyList confirms enter is a no-op with an empty repo
// list (no quit, no cdTarget set, no panic on out-of-bounds index).
func TestHandleEnter_EmptyList(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.width = 80
	m.height = 20
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if cmd != nil {
		t.Errorf("expected nil cmd on empty list; got non-nil")
	}
	if mm.cdTarget != "" {
		t.Errorf("expected empty cdTarget; got %q", mm.cdTarget)
	}
}

// TestHandleEnter_RecordsCdTargetAndQuits is the cd-and-exit contract:
// pressing enter on a selected repo records its path on the model and
// returns tea.Quit. tui.Run prints cdTarget on stdout after the alt
// screen tears down, so a shell wrapper can `cd "$(atlas)"`.
func TestHandleEnter_RecordsCdTargetAndQuits(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.cdTarget != "/r/alpha" {
		t.Errorf("cdTarget = %q; want /r/alpha", mm.cdTarget)
	}
	if cmd == nil {
		t.Errorf("expected tea.Quit cmd; got nil")
	}
}

// TestHandleEnter_DrainsPendingSave guards the drain symmetry with
// the q-handler: if a save is in flight when the user hits enter
// (e.g. they cycled sort/grouping a moment earlier and the sticky-
// session write is still on the wire), enter must NOT fire tea.Quit
// immediately. It records cdTarget, requests the latest snapshot,
// sets quitPending, and lets handleCacheSaved fire tea.Quit when the
// queue drains. Otherwise the just-applied state can be lost or the
// cache rename can race the program exit.
func TestHandleEnter_DrainsPendingSave(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Simulate a save in flight by parking one through the
	// coordinator without completing it.
	m.saves, _ = m.saves.Request(m.cache.Snapshot())

	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if mm.cdTarget != "/r/alpha" {
		t.Errorf("cdTarget = %q; want /r/alpha", mm.cdTarget)
	}
	if !mm.quitPending {
		t.Errorf("expected quitPending=true so handleCacheSaved fires tea.Quit; got false")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd while save drains; got non-nil")
	}
}

// TestHandleRefreshStarted_StaleGenIgnored guards the gen check in
// handleRefreshStarted: messages from older refresh generations don't
// stomp the current state.
func TestHandleRefreshStarted_StaleGenIgnored(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.refreshGen = 5
	m.refreshing = true
	m.refreshTotal = 100

	stream := make(chan repo.Repo, 1)
	out, cmd := m.Update(refreshStartedMsg{gen: 2, ch: stream, total: 50})
	mm := out.(Model)
	if mm.refreshTotal != 100 {
		t.Errorf("stale gen msg overwrote refreshTotal: %d", mm.refreshTotal)
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for stale-gen msg; got non-nil")
	}
	close(stream)
}

func TestHandleRefreshStarted_EmptyAdvancesPhase(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.refreshGen = 3
	m.refreshing = true

	stream := make(chan repo.Repo)
	close(stream)
	out, cmd := m.Update(refreshStartedMsg{gen: 3, ch: stream, total: 0})
	mm := out.(Model)
	// total=0 → advanceAfterPhase: with no pendingStatusPass, we drop out
	// of the refresh state machine (still potentially issue saveCacheCmd).
	if mm.refreshing {
		t.Errorf("expected refreshing=false after empty refreshStarted")
	}
	// cmd may be non-nil (a save), or nil — either is acceptable. Confirm
	// it's not panicking and state is consistent.
	_ = cmd
}

func TestHandleRefreshDone_StaleGenIgnored(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.refreshGen = 7
	m.refreshing = true
	m.refreshTotal = 50
	out, cmd := m.Update(refreshDoneMsg{gen: 4})
	mm := out.(Model)
	if !mm.refreshing {
		t.Errorf("stale-gen refreshDoneMsg should not clear refreshing")
	}
	if cmd != nil {
		t.Errorf("expected nil cmd for stale-gen refreshDoneMsg")
	}
}

func TestAdvanceAfterPhase_RunsStatusPass(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.refreshGen = 1
	m.refreshing = true
	m.pendingStatusPass = []repo.Repo{repos[0]}
	out, cmd := m.advanceAfterPhase()
	mm := out.(Model)
	if mm.pendingStatusPass != nil {
		t.Errorf("pendingStatusPass should be cleared after starting status pass")
	}
	if mm.refreshGen != 2 {
		t.Errorf("expected refreshGen bumped to 2; got %d", mm.refreshGen)
	}
	if cmd == nil {
		t.Errorf("expected a status-refresh start cmd")
	}
}

func TestStartFullRefresh_BumpsGenAndDispatches(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.refreshGen = 5
	// Simulate an in-flight refresh so we can assert startFullRefresh
	// invalidates it (cancels the worker context, bumps the gen) before
	// dispatching the rediscovery.
	cancelled := false
	m.refreshCancel = func() { cancelled = true }

	// startFullRefresh dispatches a forced discovery and immediately
	// invalidates any prior refresh; refreshTotal is established later in
	// handleDiscovered.
	out, cmd := m.startFullRefresh()
	mm := out.(Model)
	if !mm.refreshing {
		t.Errorf("expected refreshing=true after startFullRefresh")
	}
	if cmd == nil {
		t.Fatalf("expected a discover cmd")
	}
	if !cancelled {
		t.Errorf("startFullRefresh should cancel the in-flight refresh context")
	}
	if mm.refreshCancel != nil {
		t.Errorf("startFullRefresh should clear refreshCancel after cancelling")
	}
	if mm.refreshGen != 6 {
		t.Errorf("startFullRefresh should bump refreshGen to invalidate prior results; got %d", mm.refreshGen)
	}

	// The forced discovery lands: handleDiscovered force-refreshes every
	// surviving repo, bumping the gen again for the read phase and setting
	// refreshTotal to the full scoped count.
	out, cmd = mm.Update(discoveredMsg{paths: []string{"/r/alpha", "/r/beta"}, forceFull: true})
	mm = out.(Model)
	if mm.refreshGen != 7 {
		t.Errorf("handleDiscovered should bump refreshGen for the read phase; got %d", mm.refreshGen)
	}
	if mm.refreshTotal != 2 {
		t.Errorf("expected refreshTotal=2; got %d", mm.refreshTotal)
	}
	if cmd == nil {
		t.Errorf("expected a refresh start cmd")
	}
}

// TestStartFullRefresh_StaleWorkerCannotResurrectPruned guards the window
// where r is pressed while an earlier refresh is still streaming and the
// forced scan finds nothing left on disk. Because startFullRefresh bumps
// the generation immediately, a late repoRefreshedMsg from the prior
// refresh is dropped instead of reinserting a repo Reconcile just pruned.
func TestStartFullRefresh_StaleWorkerCannotResurrectPruned(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.refreshGen = 3 // the in-flight refresh's generation

	// Press r: invalidates gen 3 and dispatches a forced discovery.
	out, _ := m.startFullRefresh()
	mm := out.(Model)

	// The scan comes back empty (everything deleted): Reconcile prunes
	// every entry and handleDiscovered takes the "nothing to do" branch
	// (no further gen bump).
	out, _ = mm.Update(discoveredMsg{paths: nil, forceFull: true})
	mm = out.(Model)
	if len(mm.cache.Repos) != 0 {
		t.Fatalf("expected all repos pruned; cache still has %d", len(mm.cache.Repos))
	}

	// A straggler result from the superseded refresh (gen 3) arrives. It
	// must be dropped, not reinserted into the freshly-pruned cache.
	out, _ = mm.Update(repoRefreshedMsg{gen: 3, repo: sampleRepo("alpha", "/r/alpha", "main", 1, false)})
	mm = out.(Model)
	if _, ok := mm.cache.Repos["/r/alpha"]; ok {
		t.Errorf("stale worker result resurrected pruned repo /r/alpha")
	}
	if len(mm.cache.Repos) != 0 {
		t.Errorf("cache should remain empty; got %d entries", len(mm.cache.Repos))
	}
}

// TestStartFullRefresh_PrunesDeletedRepos is the regression guard for the
// tombstone bug: a repo deleted from disk while atlas is running must be
// dropped from the cache and the table on the next r refresh — not re-read
// into an Err record that lingers as a "!"/"—" row. The fix routes r
// through a fresh discovery so handleDiscovered's Reconcile prunes
// gone-from-disk entries (and picks up newly-added ones).
func TestStartFullRefresh_PrunesDeletedRepos(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
		sampleRepo("gamma", "/r/gamma", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80 // single-pane: no detail/legend, so "!" only appears on Err rows
	m.height = 20

	// Press r → forced discovery. beta has been deleted from disk and a new
	// delta has appeared, so the scan returns alpha, gamma, delta (no beta).
	out, _ := m.startFullRefresh()
	out, _ = out.(Model).Update(discoveredMsg{
		paths:     []string{"/r/alpha", "/r/gamma", "/r/delta"},
		forceFull: true,
	})
	mm := out.(Model)

	// The deleted repo is pruned from the cache...
	if _, ok := mm.cache.Repos["/r/beta"]; ok {
		t.Errorf("deleted repo /r/beta should be pruned from the cache")
	}
	// ...and from the rendered table (rebuildRepos ran inside handleDiscovered).
	for _, r := range mm.repos {
		if r.Path == "/r/beta" {
			t.Errorf("deleted repo /r/beta should not be in m.repos")
		}
	}
	view := mm.View()
	if strings.Contains(view, "beta") {
		t.Errorf("deleted repo should not appear in the view; got:\n%s", view)
	}
	if strings.Contains(view, "!") {
		t.Errorf("no tombstoned error row expected; got:\n%s", view)
	}
	// The newly-discovered delta is force-queued for a full read, so the
	// refresh covers all three surviving/new paths (not the two survivors).
	if mm.refreshTotal != 3 {
		t.Errorf("refreshTotal = %d; want 3 (survivors + newly-discovered repo)", mm.refreshTotal)
	}
}

// TestCacheSavedFailureStillDispatchesPending guards the error+queue path:
// a failed save should still allow the queued pending snapshot to dispatch
// rather than getting stuck behind the error.
func TestCacheSavedFailureStillDispatchesPending(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	m.saves.inFlight = true
	queued := cache.New()
	queued.Repos["/queued"] = repo.Repo{Path: "/queued"}
	m.saves.pending = queued

	out, cmd := m.Update(cacheSavedMsg{err: errIO})
	mm := out.(Model)
	if mm.statusMsg == "" {
		t.Errorf("expected error message in status bar")
	}
	if !mm.saves.InFlight() {
		t.Errorf("queued save should have been dispatched (InFlight=true)")
	}
	if mm.saves.pending != nil {
		t.Errorf("pending should be cleared after dispatch")
	}
	if cmd == nil {
		t.Errorf("expected a Batch with clearStatus + saveCacheCmd")
	}
}

// errIO is a small sentinel error used to drive cacheSavedMsg failure
// branches without importing a heavyweight error package.
type errIOType struct{}

func (errIOType) Error() string { return "i/o error" }

var errIO = errIOType{}

// ---- M3 behavior: filter, sort, group, vim nav ----

func TestFilter_LiveTypingNarrowsView(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/p/alpha", "main", 1, false),
		sampleRepo("atlas", "/p/atlas", "main", 2, false),
		sampleRepo("beta", "/p/beta", "main", 3, false),
	}
	m := newTestModel(t, repos, "/p")
	m.width = 80
	m.height = 20

	// Press '/' — should enter filter mode.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	mm := out.(Model)
	if !mm.filterMode {
		t.Fatalf("expected filterMode=true after /")
	}

	// Type "atl" — bubbles textinput consumes one rune at a time.
	for _, r := range "atl" {
		out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		mm = out.(Model)
	}
	if mm.filterText != "atl" {
		t.Errorf("filterText = %q; want %q", mm.filterText, "atl")
	}
	if len(mm.repos) != 1 || mm.repos[0].Name != "atlas" {
		t.Errorf("expected repos to narrow to atlas; got %v", mm.repos)
	}

	// Esc — should clear and exit filter mode.
	out, _ = mm.Update(tea.KeyMsg{Type: tea.KeyEsc})
	mm = out.(Model)
	if mm.filterMode {
		t.Errorf("expected filterMode=false after esc")
	}
	if mm.filterText != "" {
		t.Errorf("expected filterText cleared; got %q", mm.filterText)
	}
	if len(mm.repos) != 3 {
		t.Errorf("expected full set restored; got %d repos", len(mm.repos))
	}
}

func TestFilter_ZeroMatchPlaceholder(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/p/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/p")
	m.width = 80
	m.height = 20

	// Enter filter mode the proper way ('/'), then type a query that
	// matches nothing. The bubbles textinput needs Focus, which
	// enterFilterMode does for us.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "xyz" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	if m.selected != -1 {
		t.Errorf("zero-match should set selected=-1; got %d (filterText=%q repos=%v)", m.selected, m.filterText, m.repos)
	}
	view := m.View()
	if !strings.Contains(view, "(no matches)") {
		t.Errorf("expected (no matches) placeholder; got:\n%s", view)
	}

	// Exit filter mode via enter, then verify enter is a no-op (no
	// selection means no cdTarget and no quit cmd). `r` is now
	// "refresh all" and fires regardless of selection, so we no
	// longer assert it returns nil here.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if m.filterMode {
		t.Errorf("expected enter to exit filter mode")
	}
	if m.selected != -1 {
		t.Errorf("expected selected to remain -1 after exit; got %d", m.selected)
	}
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	mm := out.(Model)
	if cmd != nil || mm.cdTarget != "" {
		t.Errorf("expected enter to no-op when nothing selected; cmd=%v cdTarget=%q", cmd, mm.cdTarget)
	}
}

func TestSort_CycleAndReverse(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 200
	m.height = 20

	// Defaults: last_commit_at desc.
	if m.sortBy != "last_commit_at" || !m.sortDesc {
		t.Fatalf("default sort wrong: by=%s desc=%v", m.sortBy, m.sortDesc)
	}

	// 's' toggles to "repo".
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = out.(Model)
	if m.sortBy != "repo" {
		t.Errorf("after s: sortBy=%s; want repo", m.sortBy)
	}
	// 's' again → toggles back to "last_commit_at".
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = out.(Model)
	if m.sortBy != "last_commit_at" {
		t.Errorf("after s s: sortBy=%s; want last_commit_at", m.sortBy)
	}

	// 'S' toggles direction.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = out.(Model)
	if m.sortDesc {
		t.Errorf("after S: sortDesc=true; want false")
	}
}

func TestGroup_CycleFromActivityVisitsAllModes(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/projects/go/alpha", "main", 1, false),
		sampleRepo("beta", "/projects/go/beta", "main", 2, false),
		sampleRepo("gamma", "/projects/ruby/gamma", "main", 3, false),
	}
	m := newTestModel(t, repos, "/projects")
	m.width = 200
	m.height = 30

	if m.groupBy != "activity" {
		t.Fatalf("default groupBy = %s; want activity (smart default)", m.groupBy)
	}
	// Press tab once: activity → top_dir.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "top_dir" {
		t.Fatalf("after first tab: groupBy=%s; want top_dir", m.groupBy)
	}
	// With groupBy=top_dir, expect group headers for "go" and "ruby".
	view := m.View()
	if !strings.Contains(view, "go (2)") {
		t.Errorf("expected ▸ go (2) header; got:\n%s", view)
	}
	if !strings.Contains(view, "ruby (1)") {
		t.Errorf("expected ▸ ruby (1) header; got:\n%s", view)
	}

	// tab → language.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "language" {
		t.Fatalf("after second tab: groupBy=%s; want language", m.groupBy)
	}
	// tab → worktree.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "worktree" {
		t.Fatalf("after third tab: groupBy=%s; want worktree", m.groupBy)
	}
	// tab → none.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "none" {
		t.Fatalf("after fourth tab: groupBy=%s; want none", m.groupBy)
	}
	view = m.View()
	if strings.Contains(view, "go (2)") {
		t.Errorf("expected no group header in none mode; got:\n%s", view)
	}
	// tab → wraps back to activity.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "activity" {
		t.Fatalf("after fifth tab: groupBy=%s; want activity (wrap)", m.groupBy)
	}
}

// TestVim_GJumpsToLastRepo: capital G is the vim-canonical "jump to
// bottom" binding. Lowercase g lands on the first repo.
func TestVim_GJumpsToLastRepo(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("a0", "/r/a0", "main", 1, false),
		sampleRepo("a1", "/r/a1", "main", 2, false),
		sampleRepo("a2", "/r/a2", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'G'}})
	m = out.(Model)
	if m.selected != len(m.repos)-1 {
		t.Errorf("G should jump to last repo (idx %d); got %d", len(m.repos)-1, m.selected)
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	m = out.(Model)
	if m.selected != 0 {
		t.Errorf("g should jump to first repo; got %d", m.selected)
	}
}

func TestVim_HalfPageScroll(t *testing.T) {
	repos := make([]repo.Repo, 30)
	for i := range repos {
		repos[i] = sampleRepo(
			fmt.Sprintf("repo%02d", i),
			fmt.Sprintf("/r/repo%02d", i),
			"main",
			i,
			false,
		)
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 12 // viewport = 12 - 2 - 1 = 9 rows; half = 4

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyCtrlD})
	m = out.(Model)
	if m.selected == 0 {
		t.Errorf("ctrl+d should advance selection; still at %d", m.selected)
	}

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyCtrlU})
	m = out.(Model)
	if m.selected != 0 {
		t.Errorf("ctrl+u should land back at 0; got %d", m.selected)
	}
}

// TestGroup_BucketingClustersSameKey guards the P2 fix: when sort is
// last_commit_at desc and groupBy=top_dir, repos from interleaved top
// dirs must render as single contiguous blocks per group, not as
// repeated `▸ go (N)` headers every time the date order crosses a
// different directory. Within each group block, the primary sort still
// applies.
func TestGroup_BucketingClustersSameKey(t *testing.T) {
	// Date layout (days ago): g1=1, r1=2, g2=3, r2=4, g3=5. Sorted desc
	// by last_commit_at, the unbucketed order would interleave:
	// [g1, r1, g2, r2, g3]. After bucketing we expect [g1, g2, g3, r1, r2].
	repos := []repo.Repo{
		sampleRepo("g1", "/projects/go/g1", "main", 1, false),
		sampleRepo("r1", "/projects/ruby/r1", "main", 2, false),
		sampleRepo("g2", "/projects/go/g2", "main", 3, false),
		sampleRepo("r2", "/projects/ruby/r2", "main", 4, false),
		sampleRepo("g3", "/projects/go/g3", "main", 5, false),
	}
	m := newTestModel(t, repos, "/projects")
	m.width = 200
	m.height = 30
	// Test focuses on top_dir bucketing — flip from the default (activity).
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)
	if m.groupBy != "top_dir" {
		t.Fatalf("expected groupBy=top_dir after one tab; got %s", m.groupBy)
	}

	wantOrder := []string{"g1", "g2", "g3", "r1", "r2"}
	for i, want := range wantOrder {
		if i >= len(m.repos) || m.repos[i].Name != want {
			t.Errorf("m.repos[%d].Name = %q; want %q (full: %v)", i, m.repos[i].Name, want, repoNames(m.repos))
		}
	}

	// View: single `go (3)` header, then 3 go repos, then a single
	// `ruby (2)` header, then 2 ruby repos. No repeated headers.
	view := m.View()
	if got := strings.Count(view, "go (3)"); got != 1 {
		t.Errorf("expected exactly one `go (3)` header; got %d in:\n%s", got, view)
	}
	if got := strings.Count(view, "ruby (2)"); got != 1 {
		t.Errorf("expected exactly one `ruby (2)` header; got %d in:\n%s", got, view)
	}
	// `go (1)` should never appear — would indicate a fragmented header.
	if strings.Contains(view, "go (1)") {
		t.Errorf("found fragmented `go (1)` header — bucketing not applied:\n%s", view)
	}
}

func repoNames(rs []repo.Repo) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Name
	}
	return out
}

// TestStartFullRefresh_RefreshesAllUnderRoot guards the P2 fix:
// pressing R must refresh every repo under root, not just the currently
// visible (filtered) rows. Otherwise hidden repos silently miss refreshes
// and a no-match filter would issue a zero-item refresh.
func TestStartFullRefresh_RefreshesAllUnderRoot(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
		sampleRepo("gamma", "/r/gamma", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Apply a narrow filter via the proper key path so filterMode runs.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "alpha" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	if len(m.repos) != 1 {
		t.Fatalf("expected 1 visible repo; got %d", len(m.repos))
	}
	// Exit filter mode (enter) — the filter stays applied.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)

	// Trigger startFullRefresh: it dispatches a forced discovery rather
	// than reading the visible set.
	upd, cmd := m.startFullRefresh()
	mm := upd.(Model)
	if cmd == nil {
		t.Fatalf("expected a discover cmd")
	}

	// A real scan returns every repo under root regardless of the active
	// filter; model that by feeding all three discovered paths. refreshTotal
	// must reflect the *unfiltered* scoped set (3 repos), not the visible 1.
	upd, cmd = mm.Update(discoveredMsg{
		paths:     []string{"/r/alpha", "/r/beta", "/r/gamma"},
		forceFull: true,
	})
	mm = upd.(Model)
	if mm.refreshTotal != 3 {
		t.Errorf("refreshTotal = %d; want 3 (refresh-all should ignore the active filter)", mm.refreshTotal)
	}
	if cmd == nil {
		t.Errorf("expected a refresh start cmd")
	}
}

// ---- M5 behavior: detail pane, help overlay, clipboard, open ----

// fakeClipboard records what was written so tests can assert without
// touching the host clipboard.
type fakeClipboard struct{ text string }

func (f *fakeClipboard) Write(text string) error {
	f.text = text
	return nil
}

func TestCopyPath_WritesSelectedRepoPath(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	fc := &fakeClipboard{}
	m.clipboard = fc
	m.width = 200
	m.height = 20

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mm := out.(Model)
	if fc.text != "/r/alpha" {
		t.Errorf("clipboard got %q; want /r/alpha", fc.text)
	}
	if mm.statusMsg != "copied path" {
		t.Errorf("status = %q; want \"copied path\"", mm.statusMsg)
	}
}

func TestCopyPath_NoSelectionStatusErr(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	fc := &fakeClipboard{}
	m.clipboard = fc
	m.width = 80
	m.height = 20

	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	mm := out.(Model)
	if fc.text != "" {
		t.Errorf("clipboard should not have been written; got %q", fc.text)
	}
	if !mm.statusIsErr {
		t.Errorf("expected error status; got %q (isErr=%v)", mm.statusMsg, mm.statusIsErr)
	}
}

func TestOpenOrigin_NoOriginShowsStatus(t *testing.T) {
	r := sampleRepo("alpha", "/r/alpha", "main", 1, false)
	r.OriginURL = ""
	m := newTestModel(t, []repo.Repo{r}, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'o'}})
	mm := out.(Model)
	if mm.statusMsg != "no origin URL" || !mm.statusIsErr {
		t.Errorf("expected 'no origin URL' err status; got %q (isErr=%v)", mm.statusMsg, mm.statusIsErr)
	}
}

func TestHelpOverlay_ToggleAndDismiss(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 200
	m.height = 30

	// `?` opens.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	m = out.(Model)
	if !m.showHelp {
		t.Fatalf("expected showHelp=true after ?")
	}
	if !strings.Contains(m.View(), "atlas — keys") {
		t.Errorf("expected help overlay text in view")
	}
	// esc closes.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(Model)
	if m.showHelp {
		t.Errorf("expected showHelp=false after esc")
	}
}

// The help overlay must list the flag glyphs so the ? affordance is
// the canonical reference for what every column symbol means.
func TestHelpOverlay_IncludesFlagsLegend(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 200
	m.height = 40
	m.showHelp = true

	view := m.View()
	if !strings.Contains(view, "Flags:") {
		t.Errorf("expected 'Flags:' section in help overlay; got:\n%s", view)
	}
	for _, glyph := range []string{"*", "?", "▲", "⊘", "↑", "↓", "≡", "!"} {
		if !strings.Contains(view, glyph) {
			t.Errorf("help overlay missing glyph %q in flags section", glyph)
		}
	}
}

// At narrow widths (50–60 cols) the 2-col keybind layout would push
// the help overlay past the terminal edge. viewHelp must fall back
// to a 1-col layout so no rendered line exceeds the terminal width.
func TestHelpOverlay_NarrowFallbackTo1Col(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 55
	m.height = 40
	m.showHelp = true

	view := m.View()
	for _, line := range strings.Split(view, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("help overlay line wider than terminal (%d > %d): %q",
				w, m.width, line)
		}
	}
	// All keybinds still present after collapsing to 1 column.
	for _, want := range []string{"k", "j", "ctrl+u", "ctrl+d", "/", "tab", "enter"} {
		if !strings.Contains(view, want) {
			t.Errorf("help overlay missing key %q in 1-col fallback", want)
		}
	}
}

// The legend bottom-anchors below the detail pane when there's room.
// At 200×40 the right column is tall enough for detail content + a
// blank spacer + the 5-line legend.
func TestView_LegendDocksWithDetailPane(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 200
	m.height = 40
	m.scanning = false

	view := m.View()
	if !strings.Contains(view, "▸ Flags") {
		t.Errorf("expected '▸ Flags' legend header in view at 200x40; got:\n%s", view)
	}
	for _, entry := range []string{"dirty", "untracked", "stale", "lagging", "ahead", "behind", "stashed", "error"} {
		if !strings.Contains(view, entry) {
			t.Errorf("legend missing entry %q in view at 200x40", entry)
		}
	}
}

// A short terminal can't fit detail + spacer + legend; the legend
// must collapse entirely (not partially) so the detail pane keeps
// its space.
func TestView_LegendCollapsesWhenTight(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 200
	m.height = 12
	m.scanning = false

	view := m.View()
	if strings.Contains(view, "▸ Flags") {
		t.Errorf("legend should be hidden when bodyHeight too small for detail + legend; got:\n%s", view)
	}
}

// Single-pane mode (<100 cols) hides the detail pane entirely; the
// legend rides along with it so it must be hidden too.
func TestView_LegendAbsentInSinglePane(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.width = 80
	m.height = 40
	m.scanning = false

	view := m.View()
	if strings.Contains(view, "▸ Flags") {
		t.Errorf("legend should not render below the detail-pane threshold; got:\n%s", view)
	}
}

func TestRecentCommitsTick_SupersededIsDropped(t *testing.T) {
	r := sampleRepo("alpha", "/r/alpha", "main", 1, false)
	m := newTestModel(t, []repo.Repo{r}, "/r")

	// gen=5 is current; tick gen=3 is superseded.
	m.recentGen = 5
	out, cmd := m.Update(recentCommitsTickMsg{gen: 3, path: "/r/alpha"})
	mm := out.(Model)
	if cmd != nil {
		t.Errorf("superseded tick should not dispatch; got cmd")
	}
	if _, present := mm.recentCommits["/r/alpha"]; present {
		t.Errorf("superseded tick should not write a placeholder")
	}
}

func TestRecentCommitsTick_CurrentDispatches(t *testing.T) {
	r := sampleRepo("alpha", "/r/alpha", "main", 1, false)
	m := newTestModel(t, []repo.Repo{r}, "/r")
	m.recentGen = 2
	out, cmd := m.Update(recentCommitsTickMsg{gen: 2, path: "/r/alpha"})
	mm := out.(Model)
	if cmd == nil {
		t.Errorf("current tick should dispatch a fetch cmd")
	}
	state, ok := mm.recentCommits["/r/alpha"]
	if !ok || !state.loading || state.loaded {
		t.Errorf("expected loading state reserved for the slot; got map[%q]=%+v ok=%v", "/r/alpha", state, ok)
	}
}

func TestRecentCommitsLoaded_CachesOnSuccess(t *testing.T) {
	m := newTestModel(t, []repo.Repo{sampleRepo("a", "/r/a", "main", 1, false)}, "/r")
	out, _ := m.Update(recentCommitsLoadedMsg{path: "/r/a", lines: []string{"first", "second"}})
	mm := out.(Model)
	got := mm.recentCommits["/r/a"]
	if !got.loaded || len(got.lines) != 2 || got.lines[0] != "first" || got.lines[1] != "second" {
		t.Errorf("expected loaded state with subjects; got %+v", got)
	}
}

// TestRecentCommitsLoaded_EmptyRepoIsLoadedNotLoading guards the
// empty-repo lifecycle: git.RecentCommits returns (nil, nil) for an
// empty repo, and the handler must transition to loaded=true with an
// empty (but non-nil) slice so the detail pane shows (no commits)
// rather than staying stuck at (loading…).
func TestRecentCommitsLoaded_EmptyRepoIsLoadedNotLoading(t *testing.T) {
	m := newTestModel(t, []repo.Repo{sampleRepo("a", "/r/a", "main", 1, false)}, "/r")
	out, _ := m.Update(recentCommitsLoadedMsg{path: "/r/a", lines: nil, err: nil})
	mm := out.(Model)
	state := mm.recentCommits["/r/a"]
	if !state.loaded {
		t.Errorf("empty-repo load should set loaded=true; got %+v", state)
	}
	if state.loading {
		t.Errorf("loading should be false after load; got %+v", state)
	}
	if state.err != nil {
		t.Errorf("expected nil err for empty repo; got %v", state.err)
	}
}

// TestRecentCommitsLoaded_ErrorTransitionsToLoadedWithErr verifies
// that a load error becomes a loaded-with-err state so the detail pane
// can show "(commits unavailable)" and so subsequent ticks for the
// same path don't re-fetch (loading || loaded short-circuits).
func TestRecentCommitsLoaded_ErrorTransitionsToLoadedWithErr(t *testing.T) {
	m := newTestModel(t, []repo.Repo{sampleRepo("a", "/r/a", "main", 1, false)}, "/r")
	out, _ := m.Update(recentCommitsLoadedMsg{path: "/r/a", err: errIO})
	mm := out.(Model)
	state := mm.recentCommits["/r/a"]
	if !state.loaded || state.err == nil {
		t.Errorf("error load should set loaded=true with err; got %+v", state)
	}
}

func TestRefreshInvalidatesRecentCommits(t *testing.T) {
	m := newTestModel(t, []repo.Repo{sampleRepo("a", "/r/a", "main", 1, false)}, "/r")
	m.recentCommits["/r/a"] = recentCommitsState{loaded: true, lines: []string{"old"}}

	updated := sampleRepo("a", "/r/a", "main", 0, true)
	stream := make(chan repo.Repo, 1)
	m.refreshGen = 1
	m.refreshing = true
	m.refreshTotal = 1
	m.activeCh = stream
	out, _ := m.Update(repoRefreshedMsg{gen: 1, repo: updated})
	mm := out.(Model)
	if _, present := mm.recentCommits["/r/a"]; present {
		t.Errorf("refresh should invalidate recentCommits[/r/a]")
	}
	close(stream)
}

// TestInit_KickoffSchedulesAcceptedTick is the end-to-end regression
// for the warm-launch detail-pane load: Init's kickoff path must
// produce a tick whose gen actually matches the live model when it
// arrives. Init has a value receiver, so a previous version that
// called scheduleRecentCommitsLoad directly from Init would bump the
// copy's recentGen, leave the live model's recentGen unchanged, and
// have its tick rejected as stale. This test catches that regression
// by feeding Init's actual emitted message into the same model.
func TestInit_KickoffSchedulesAcceptedTick(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	if m.selectedPath == "" {
		t.Fatalf("precondition: warm cache must populate selectedPath")
	}
	// Init returns tea.Batch(discoverCmd, kickoffCmd). Drain the
	// batch by walking it via tea's BatchMsg unwrap helper isn't
	// public, so we instead invoke the kickoff explicitly: send
	// initialLoadMsg into Update and assert the resulting tick is
	// scheduled against the live model.
	out, kickoffCmd := m.Update(initialLoadMsg{})
	mm := out.(Model)
	if kickoffCmd == nil {
		t.Fatalf("initialLoadMsg should produce a tick cmd")
	}
	// Run the tick cmd — it should yield a recentCommitsTickMsg whose
	// gen matches the live model's recentGen so the next Update
	// dispatches the fetch instead of dropping it as stale.
	tickMsg := kickoffCmd()
	tick, ok := tickMsg.(recentCommitsTickMsg)
	if !ok {
		t.Fatalf("expected recentCommitsTickMsg; got %T", tickMsg)
	}
	if tick.gen != mm.recentGen {
		t.Errorf("tick gen %d != live model recentGen %d — Init mutated a copy?", tick.gen, mm.recentGen)
	}
	if tick.path != mm.selectedPath {
		t.Errorf("tick path %q != selectedPath %q", tick.path, mm.selectedPath)
	}
	// Feed the tick back through Update against the live model — it
	// must be accepted (not stale) and dispatch the fetch.
	out2, fetchCmd := mm.Update(tick)
	mm2 := out2.(Model)
	if fetchCmd == nil {
		t.Fatalf("live-model tick should be accepted and dispatch fetch")
	}
	state := mm2.recentCommits[mm.selectedPath]
	if !state.loading {
		t.Errorf("expected loading=true placeholder after accepted tick; got %+v", state)
	}
}

// TestRebuildRepos_NewSelectedPathDispatchesLoad covers the
// no-navigation-key path: a sort flip can move a different repo into
// the same `selected` index, transitioning selectedPath without any
// j/k. rebuildRepos must return a tea.Cmd to load that path's
// commits — otherwise the detail pane stays stale.
func TestRebuildRepos_NewSelectedPathDispatchesLoad(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 5, false),
	}
	m := newTestModel(t, repos, "/r")
	// Default sort = last_commit_at desc → alpha (1d) first, beta (5d)
	// second. selectedPath = /r/alpha. Drop the prior selection so
	// rebuildRepos sees a transition from "" → /r/beta below.
	m.selectedPath = ""
	prevGen := m.recentGen
	cmd := (&m).rebuildRepos()
	if cmd == nil {
		t.Errorf("rebuildRepos should return a tick cmd when selectedPath transitions")
	}
	if m.recentGen <= prevGen {
		t.Errorf("recentGen should advance: prev=%d now=%d", prevGen, m.recentGen)
	}
}

// TestScheduleLoad_CachedShortCircuitStillBumpsGen guards the P3
// fix: moving from an uncached repo (with a pending tick) to a
// cached repo must bump recentGen so the older tick doesn't dispatch
// a fetch for the now-deselected repo.
func TestScheduleLoad_CachedShortCircuitStillBumpsGen(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}, "/r")
	// alpha has a pending in-flight tick (we just selected it).
	m.selected = 0
	m.selectedPath = "/r/alpha"
	(&m).scheduleRecentCommitsLoad()
	genAfterAlpha := m.recentGen

	// beta is already cached → moving to it short-circuits the tick.
	m.recentCommits["/r/beta"] = recentCommitsState{
		loaded: true,
		lines:  []string{"already cached"},
	}
	m.selected = 1
	m.selectedPath = "/r/beta"
	cmd := (&m).scheduleRecentCommitsLoad()
	if cmd != nil {
		t.Errorf("cached selection should not schedule a tick; got non-nil")
	}
	if m.recentGen <= genAfterAlpha {
		t.Errorf("recentGen must advance even when short-circuiting cached path: prev=%d now=%d", genAfterAlpha, m.recentGen)
	}
}

// TestRecentCommitsLoaded_EmptyRepoEndsAtNoCommitsInDetail is the
// integration version of the empty-repo lifecycle: simulate a fetch
// returning (nil, nil) and assert the *rendered* detail pane shows
// "(no commits)" rather than "(loading…)".
func TestRecentCommitsLoaded_EmptyRepoEndsAtNoCommitsInDetail(t *testing.T) {
	r := sampleRepo("empty", "/r/empty", "main", 1, false)
	r.LastCommitAt = nil
	m := newTestModel(t, []repo.Repo{r}, "/r")
	m.width = 200
	m.height = 30
	out, _ := m.Update(recentCommitsLoadedMsg{path: "/r/empty", lines: nil})
	mm := out.(Model)
	view := mm.View()
	if !strings.Contains(view, "(no commits)") {
		t.Errorf("expected (no commits) in detail pane after empty load; got:\n%s", view)
	}
	if strings.Contains(view, "(loading…)") {
		t.Errorf("detail pane should not be stuck at (loading…) after load; got:\n%s", view)
	}
}

// TestM4FlagGlyphs verifies the table renders ↑N/↓N/≡N for repos with
// upstream divergence and stashes, and that ▲ shows for stale repos.
// All four signals come from AnnotateDerived + persisted reader fields.
func TestM4FlagGlyphs(t *testing.T) {
	r := sampleRepo("svc", "/r/svc", "main", 1, false)
	r.AheadOrigin = 2
	r.BehindOrigin = 1
	r.StashCount = 3
	m := newTestModel(t, []repo.Repo{r}, "/r")
	m.width = 200
	m.height = 20

	out := m.View()
	for _, glyph := range []string{"↑2", "↓1", "≡3"} {
		if !strings.Contains(out, glyph) {
			t.Errorf("expected glyph %q in render; got:\n%s", glyph, out)
		}
	}
}

func TestM4StaleGlyph(t *testing.T) {
	// 100 days old with default StaleDays=60 → Stale=true via annotate.
	old := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC).Add(-100 * 24 * time.Hour)
	r := sampleRepo("dormant", "/r/dormant", "main", 100, false)
	r.LastCommitAt = &old
	m := newTestModel(t, []repo.Repo{r}, "/r")
	m.width = 200
	m.height = 20

	out := m.View()
	if !strings.Contains(out, "▲") {
		t.Errorf("expected ▲ stale glyph; got:\n%s", out)
	}
}

func TestSelectedPathSurvivesRefresh(t *testing.T) {
	// When the cache shifts the position of the selected repo (e.g., after
	// a re-sort triggered by metadata change), the selection should track
	// the path, not the index.
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
		sampleRepo("gamma", "/r/gamma", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20

	// Move down to beta.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyDown})
	m = out.(Model)
	if m.repos[m.selected].Name != "beta" {
		t.Fatalf("expected beta selected; got %s", m.repos[m.selected].Name)
	}

	// Toggle sort direction — now ascending: gamma is at index 0, beta at
	// 1, alpha at 2. selectedPath should keep us on beta.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'S'}})
	m = out.(Model)
	if m.repos[m.selected].Name != "beta" {
		t.Errorf("after sort flip: expected still on beta; got %s", m.repos[m.selected].Name)
	}
}

// TestStickySession_PersistsSortAndGroup confirms cycling sort and
// grouping mutates m.cache.Session in place (so the next cache save
// flushes the new state) and that a subsequent New() reads it back.
func TestStickySession_PersistsSortAndGroup(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 200
	m.height = 20

	// Cycle sort and grouping. Each press records the session.
	out, _ := m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'s'}})
	m = out.(Model)
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyTab})
	m = out.(Model)

	if m.cache.Session == nil {
		t.Fatalf("expected cache.Session to be populated")
	}
	if m.cache.Session.SortBy != m.sortBy {
		t.Errorf("Session.SortBy = %q; want %q", m.cache.Session.SortBy, m.sortBy)
	}
	if m.cache.Session.GroupBy != m.groupBy {
		t.Errorf("Session.GroupBy = %q; want %q", m.cache.Session.GroupBy, m.groupBy)
	}
	wantOrder := "asc"
	if m.sortDesc {
		wantOrder = "desc"
	}
	if m.cache.Session.SortOrder != wantOrder {
		t.Errorf("Session.SortOrder = %q; want %q", m.cache.Session.SortOrder, wantOrder)
	}

	// Re-instantiate New() with the same cache and confirm the session
	// values override the hardcoded TUI defaults.
	m2 := New(context.Background(), m.cache, m.cachePath, config.Defaults(), "/r")
	if m2.sortBy != m.sortBy {
		t.Errorf("relaunched sortBy = %q; want %q", m2.sortBy, m.sortBy)
	}
	if m2.groupBy != m.groupBy {
		t.Errorf("relaunched groupBy = %q; want %q", m2.groupBy, m.groupBy)
	}
}

// viewHeight is a tiny helper for layout-stability tests: it returns
// the count of rendered lines in m.View() at the model's current
// dimensions. Used to assert the View height is invariant across
// model state changes (filter entry, typing, list size).
func viewHeight(t *testing.T, m Model) int {
	t.Helper()
	out := m.View()
	return strings.Count(out, "\n") + 1
}

// firstNonEmptyLine returns the first line of s that has visible
// content after stripping ANSI escapes. Used to compare what's
// rendered on the status bar's top row across filter-mode toggles.
func firstNonEmptyLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(stripANSI(line)) != "" {
			return line
		}
	}
	return ""
}

// stripANSI removes CSI escape sequences so substring assertions on
// rendered output don't have to know about lipgloss's color codes.
func stripANSI(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && s[i+1] == '[' {
			j := i + 2
			for j < len(s) {
				c := s[j]
				j++
				if c >= 0x40 && c <= 0x7e {
					break
				}
			}
			i = j
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

// filterRowFromView returns the line of m.View() that corresponds
// to the dedicated filter row — always positioned right after the
// status bar block. Used by filter-row tests to assert on the row's
// content directly rather than scanning the whole view.
func filterRowFromView(m Model) string {
	lines := strings.Split(m.View(), "\n")
	idx := m.statusBarHeight()
	if idx < 0 || idx >= len(lines) {
		return ""
	}
	return lines[idx]
}

// TestView_FilterRowShowsLiveInputInMode confirms that entering
// filter mode renders the live textinput on the dedicated filter
// row (the line directly under the status bar). The status bar's
// own content stays put — no substitution — and the filter row
// shows the bubbles textinput's placeholder until the user types.
func TestView_FilterRowShowsLiveInputInMode(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	beforeHeight := viewHeight(t, m)
	if !strings.Contains(firstNonEmptyLine(m.View()), "atlas") {
		t.Fatalf("expected status bar's first line to contain 'atlas' before filter")
	}

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)

	if got := viewHeight(t, m); got != beforeHeight {
		t.Errorf("View height changed when entering filter mode: before=%d after=%d", beforeHeight, got)
	}
	// Status bar's first line should be unchanged (no substitution).
	if !strings.Contains(firstNonEmptyLine(m.View()), "atlas") {
		t.Errorf("expected status bar's first line to keep 'atlas' header after entering filter mode")
	}
	// The dedicated filter row carries the live input.
	row := stripANSI(filterRowFromView(m))
	if !strings.Contains(row, "type to filter") {
		t.Errorf("expected filter row to show textinput placeholder; got %q", row)
	}
}

// TestView_FilterRowShowsAppliedFilter checks that after the user
// types a filter and presses enter (exiting filter mode), the
// dedicated row shows the applied filter and the clear hint, so
// there's a persistent reminder that filtering is in effect.
func TestView_FilterRowShowsAppliedFilter(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("atlas", "/r/atlas", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "atl" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	// Press enter — exits filter mode but keeps the applied filter.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if m.filterMode {
		t.Fatalf("expected filter mode closed after enter")
	}
	if m.filterText != "atl" {
		t.Fatalf("expected filterText preserved as 'atl'; got %q", m.filterText)
	}

	row := stripANSI(filterRowFromView(m))
	for _, want := range []string{"filter:", "atl", "esc to clear"} {
		if !strings.Contains(row, want) {
			t.Errorf("filter row missing %q; got %q", want, row)
		}
	}
}

// TestView_FilterRowBlankWhenInactive confirms the dedicated row
// is empty (just whitespace) when no filter is applied — the
// "breathing room" between the status bar and the column headers.
func TestView_FilterRowBlankWhenInactive(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	row := stripANSI(filterRowFromView(m))
	if strings.TrimSpace(row) != "" {
		t.Errorf("expected inactive filter row to be blank; got %q", row)
	}
	// And the row must not contain any ANSI escape — otherwise the
	// gold styling is leaking when no filter is active.
	if strings.ContainsRune(filterRowFromView(m), 0x1b) {
		t.Errorf("expected inactive filter row to carry no ANSI styling; got %q", filterRowFromView(m))
	}
}

// TestStatusBar_StaleCountIncludesTransientSignal confirms that
// the "N stale" status part counts repos whose Stale flag is set
// by AnnotateDerived. Stale is a `json:"-"` transient field, so
// pulling scoped repos straight from the cache leaves it
// false-by-default — the bar would silently lose the signal.
// statusBarParts must annotate before counting.
func TestStatusBar_StaleCountIncludesTransientSignal(t *testing.T) {
	old := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	staleRepo := sampleRepo("dormant", "/r/dormant", "main", 1, false)
	staleRepo.LastCommitAt = &old

	m := newTestModel(t, []repo.Repo{staleRepo}, "/r")
	m.cfg.StaleDays = 60
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)
	out, _ = m.Update(initialLoadMsg{})
	m = out.(Model)

	parts := m.statusBarParts()
	found := false
	for _, p := range parts {
		if strings.Contains(p, "stale") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected status bar to include a 'stale' part for a >60d-old repo; got parts=%v", parts)
	}
}

// TestFilterRow_TruncatesLongAppliedFilter guards against the
// applied-filter chip wrapping when the user's filter text is
// longer than the chip can hold at the current terminal width.
// Without truncation, lipgloss wraps the chip to a second line
// and breaks the always-1-row invariant.
func TestFilterRow_TruncatesLongAppliedFilter(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	out, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = out.(Model)

	// Set a filter long enough to overflow any 40-col chip.
	m.filterText = strings.Repeat("x", 200)

	row := m.renderFilterRow(m.width)
	if lipgloss.Height(row) != 1 {
		t.Errorf("expected applied-filter chip to render on 1 line; got %d lines:\n%s",
			lipgloss.Height(row), row)
	}
	// And the visible width should fit within terminal width.
	for i, line := range strings.Split(row, "\n") {
		if w := lipgloss.Width(line); w > m.width {
			t.Errorf("line %d width %d > terminal width %d", i, w, m.width)
		}
	}
	// Truncation marker should be present.
	if !strings.Contains(stripANSI(row), "…") {
		t.Errorf("expected truncation ellipsis in chip; got %q", stripANSI(row))
	}
}

// TestFilterRow_FitsOnNarrowTerminals guards the chip's
// one-line invariant at every width — including widths so small
// that even the "filter: " prefix + " · esc to clear" suffix
// would overflow the bar's padding. At narrow widths the suffix
// is dropped and the prefix is truncated; the rendered row must
// still be exactly one line.
func TestFilterRow_FitsOnNarrowTerminals(t *testing.T) {
	m := newTestModel(t, []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}, "/r")
	m.filterText = "needle"

	for _, w := range []int{1, 5, 10, 15, 20, 24, 25, 40, 80} {
		row := m.renderFilterRow(w)
		if h := lipgloss.Height(row); h != 1 {
			t.Errorf("width=%d: expected 1-line chip; got %d lines:\n%s", w, h, row)
		}
		for i, line := range strings.Split(row, "\n") {
			if lw := lipgloss.Width(line); lw > w {
				t.Errorf("width=%d: line %d visible width %d > terminal width", w, i, lw)
			}
		}
	}
}

// TestStatusBarHeightStableWhileFiltering is a regression guard
// for the count-driven jitter that happens when statusBarParts
// aggregates over the *filtered* set: as matches narrow, signal
// parts like "5 dirty" or "1 stale" drop out, the bar reflows
// from N lines to N-1, and everything below it (the filter row,
// the table) shifts up. Asserts the status bar's rendered height
// is invariant across filter typing.
//
// The fixture deliberately exercises every signal part (dirty,
// ahead, behind, stash, stale) on the unfiltered set so the
// status bar has enough content to wrap at the narrow width
// chosen here. Filtering to "gamma" matches a single clean repo,
// dropping every signal — which is the transition that used to
// collapse the bar from N lines to fewer.
func TestStatusBarHeightStableWhileFiltering(t *testing.T) {
	now := time.Date(2026, 5, 1, 0, 0, 0, 0, time.UTC)
	mkRepo := func(name string, signals func(*repo.Repo)) repo.Repo {
		r := sampleRepo(name, "/r/"+name, "main", 1, false)
		r.LastCommitAt = &now
		signals(&r)
		return r
	}
	repos := []repo.Repo{
		mkRepo("alpha", func(r *repo.Repo) { r.Dirty = true; r.StashCount = 1 }),
		mkRepo("beta", func(r *repo.Repo) { r.AheadOrigin = 2 }),
		mkRepo("delta", func(r *repo.Repo) { r.BehindOrigin = 1; r.Stale = true }),
		mkRepo("gamma", func(r *repo.Repo) {}),
	}
	m := newTestModel(t, repos, "/r")
	out, _ := m.Update(tea.WindowSizeMsg{Width: 40, Height: 24})
	m = out.(Model)
	out, _ = m.Update(initialLoadMsg{})
	m = out.(Model)

	wantHeight := m.statusBarHeight()
	if wantHeight < 2 {
		t.Fatalf("test premise: status bar should wrap to ≥2 lines at width 40 with all signals; got %d", wantHeight)
	}

	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "gamma" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
		got := m.statusBarHeight()
		if got != wantHeight {
			t.Errorf("statusBarHeight changed after typing %q (filterText=%q, repos=%d): got %d, want %d",
				r, m.filterText, len(m.repos), got, wantHeight)
		}
	}
}

// TestFilterInputWidthFitsBar is a regression guard for the wrap
// bug where ti.Width was sized via WindowSizeMsg without leaving
// enough headroom for everything bubbles + the filterBarActive
// style render around the typed text. When typing, the full
// visible row is:
//
//   prompt + value + cursor + padding-to-ti.Width + bar-padding
//
// which simplifies to prompt + ti.Width + 1 (cursor) inside the
// bar, then +2 for the bar's Padding(0, 1). So the invariant is
// prompt + ti.Width + 1 + 2 ≤ m.width. A miss by 1 col is enough
// to wrap the filter row to two lines visually in the terminal,
// even though lipgloss reports a single logical line.
func TestFilterInputWidthFitsBar(t *testing.T) {
	m := newTestModel(t, nil, "/r")
	out, _ := m.Update(tea.WindowSizeMsg{Width: 80, Height: 24})
	m = out.(Model)

	const barPadding = 2 // filterBarActive.Padding(0, 1)
	const cursorCell = 1 // cursor rendered past end-of-value
	promptWidth := lipgloss.Width(m.filterInput.Prompt)
	total := promptWidth + m.filterInput.Width + cursorCell + barPadding
	if total > m.width {
		t.Errorf("prompt(%d) + filterInput.Width(%d) + cursor(%d) + barPadding(%d) = %d exceeds m.width=%d — filter row will wrap",
			promptWidth, m.filterInput.Width, cursorCell, barPadding, total, m.width)
	}
}

// TestView_FilterRowDistinguishesActiveStates confirms that the
// filter row renders differently across its three states. Colors
// are stripped in non-TTY test environments, so we assert on the
// content: blank when inactive, textinput when in filter mode,
// applied-filter chip when mode is closed but filter is set. Each
// must render a unique row — same content across two states would
// mean the filter row is broken.
func TestView_FilterRowDistinguishesActiveStates(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	inactiveRow := filterRowFromView(m)

	// Filter mode open.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	openRow := filterRowFromView(m)

	// Apply (enter exits mode but keeps text).
	for _, r := range "alpha" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	appliedRow := filterRowFromView(m)

	if inactiveRow == openRow {
		t.Errorf("inactive and open filter rows identical: %q", openRow)
	}
	if openRow == appliedRow {
		t.Errorf("open and applied filter rows identical: %q", appliedRow)
	}
	if inactiveRow == appliedRow {
		t.Errorf("inactive and applied filter rows identical: %q", appliedRow)
	}
	if strings.TrimSpace(stripANSI(inactiveRow)) != "" {
		t.Errorf("expected inactive filter row to be blank; got %q", inactiveRow)
	}
}

// TestEsc_ClearsFilterButDoesNotQuit covers the two-step esc
// behavior: pressing esc when a filter is applied clears it, and
// pressing esc again (no filter active) is a no-op. esc never
// quits — only q and ctrl+c do.
func TestEsc_ClearsFilterButDoesNotQuit(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("atlas", "/r/atlas", "main", 2, false),
		sampleRepo("beta", "/r/beta", "main", 3, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 20
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	// Apply a filter via /atl<enter>.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "atl" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m = out.(Model)
	if m.filterText == "" {
		t.Fatalf("expected filter to be applied after /atl<enter>")
	}

	assertNotQuit := func(t *testing.T, cmd tea.Cmd, label string) {
		t.Helper()
		if cmd == nil {
			return
		}
		msg := cmd()
		if _, isQuit := msg.(tea.QuitMsg); isQuit {
			t.Errorf("%s returned tea.Quit — esc must not quit", label)
		}
	}

	// First esc: clears filter, does not quit.
	out, cmd := m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	m = out.(Model)
	if m.filterText != "" {
		t.Errorf("first esc should clear filter; filterText=%q", m.filterText)
	}
	assertNotQuit(t, cmd, "first esc (with filter applied)")

	// Second esc with no filter: must NOT quit. q is for quit.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyEsc})
	assertNotQuit(t, cmd, "second esc (no filter)")

	// q should still quit.
	_, cmd = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd == nil {
		t.Fatalf("q should return a command")
	}
	if msg := cmd(); msg != nil {
		if _, isQuit := msg.(tea.QuitMsg); !isQuit {
			t.Errorf("q should return tea.Quit; got %T", msg)
		}
	}
}

// TestView_FilterTypingDoesNotChangeHeight catches both fixes
// at once: the body padding (so a narrowing match set doesn't
// pull the bottom bar up) and the removal of the `filter: <text>`
// mirror from the status bar (so a growing filter string doesn't
// push the status bar to wrap). After entering filter mode and
// typing any sequence — including one that produces zero matches
// — the rendered View must stay at exactly m.height lines.
func TestView_FilterTypingDoesNotChangeHeight(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("atlas", "/r/atlas", "main", 2, false),
		sampleRepo("beta", "/r/beta", "main", 3, false),
		sampleRepo("gamma", "/r/gamma", "main", 4, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 24
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	// Enter filter mode.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)

	want := m.height
	if got := viewHeight(t, m); got != want {
		t.Fatalf("baseline View height = %d; want %d", got, want)
	}

	// Type each rune; height must stay at m.height. The "x" runs
	// produce zero matches — the `(no matches)` placeholder must
	// also fill the viewport.
	for _, r := range "atxyz" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
		if got := viewHeight(t, m); got != want {
			t.Errorf("View height changed after typing %q (filterText=%q): got %d, want %d",
				r, m.filterText, got, want)
		}
	}
}

// TestView_NoMatchesKeepsColumnHeaders confirms that filtering
// down to zero matches still shows the table's column headers
// (repo / branch / last_commit / flags), with the "(no matches)"
// placeholder sitting under them. Keeps the layout legible — the
// user sees the filter killed the rows, not the whole table.
func TestView_NoMatchesKeepsColumnHeaders(t *testing.T) {
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m := newTestModel(t, repos, "/r")
	m.width = 80
	m.height = 24
	out, _ := m.Update(initialLoadMsg{})
	m = out.(Model)

	// Enter filter mode and type a string that matches nothing.
	out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	m = out.(Model)
	for _, r := range "xyz" {
		out, _ = m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}})
		m = out.(Model)
	}

	view := m.View()
	for _, want := range []string{"repo", "branch", "last_commit", "flags", "(no matches)"} {
		if !strings.Contains(view, want) {
			t.Errorf("no-matches view missing %q; got:\n%s", want, view)
		}
	}
	// "(no matches)" should appear below the header row, not above.
	header := strings.Index(view, "last_commit")
	placeholder := strings.Index(view, "(no matches)")
	if header < 0 || placeholder < 0 || placeholder <= header {
		t.Errorf("expected (no matches) to render below column headers; header=%d placeholder=%d", header, placeholder)
	}
}

// TestView_BodyPadsToHeight ensures the bottom hint bar stays
// anchored regardless of how short the table body is. Two
// scenarios: an empty cache (placeholder copy) and a populated
// cache. Both must render at exactly m.height lines.
func TestView_BodyPadsToHeight(t *testing.T) {
	// Empty cache — the body is the multi-line empty-state copy,
	// which is shorter than the viewport.
	m := newTestModel(t, nil, "/r")
	m.width = 80
	m.height = 24
	m.scanning = false
	if got := viewHeight(t, m); got != m.height {
		t.Errorf("empty-state View height = %d; want %d", got, m.height)
	}

	// Populated cache (small).
	repos := []repo.Repo{
		sampleRepo("alpha", "/r/alpha", "main", 1, false),
		sampleRepo("beta", "/r/beta", "main", 2, false),
	}
	m2 := newTestModel(t, repos, "/r")
	m2.width = 80
	m2.height = 24
	out, _ := m2.Update(initialLoadMsg{})
	m2 = out.(Model)
	if got := viewHeight(t, m2); got != m2.height {
		t.Errorf("populated View height = %d; want %d", got, m2.height)
	}
}
