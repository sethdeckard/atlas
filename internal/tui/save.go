package tui

import "github.com/sethdeckard/atlas/internal/cache"

// saveCoordinator serializes async cache writes. At most one save runs at
// a time; further requests park their snapshot on `pending`, replacing any
// older queued snapshot. Complete is called when an in-flight save
// finishes; if a snapshot is pending it's returned for dispatch, otherwise
// the queue is empty and the caller can act on side concerns (e.g. fire
// tea.Quit).
//
// The type is a value so Bubble Tea's value-pass model semantics work
// naturally: each Update returns a new Model holding the new
// saveCoordinator state.
type saveCoordinator struct {
	inFlight bool
	pending  *cache.Cache
}

// Request handles a save request. If no save is currently in flight,
// returns the snapshot to dispatch and a coordinator that records the
// in-flight state. Otherwise parks the snapshot (replacing any older
// queued one) and returns nil — caller dispatches no command.
func (c saveCoordinator) Request(snap *cache.Cache) (saveCoordinator, *cache.Cache) {
	if c.inFlight {
		c.pending = snap
		return c, nil
	}
	c.inFlight = true
	return c, snap
}

// Complete is called when an in-flight save finishes. Returns the next
// snapshot to dispatch (nil if the queue is empty) and the updated
// coordinator state.
func (c saveCoordinator) Complete() (saveCoordinator, *cache.Cache) {
	c.inFlight = false
	if c.pending != nil {
		snap := c.pending
		c.pending = nil
		c.inFlight = true
		return c, snap
	}
	return c, nil
}

// InFlight reports whether a save is currently running. Used by the Quit
// handler to decide between sync-save-and-quit and defer-quit-until-drained.
func (c saveCoordinator) InFlight() bool { return c.inFlight }
