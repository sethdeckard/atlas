package tui

import (
	"testing"

	"github.com/sethdeckard/atlas/internal/cache"
	"github.com/sethdeckard/atlas/internal/repo"
)

func TestSaveCoordinator_RequestDispatchesWhenIdle(t *testing.T) {
	var c saveCoordinator
	snap := cache.New()
	c, out := c.Request(snap)
	if out != snap {
		t.Errorf("Request should return snap when idle; got %v", out)
	}
	if !c.InFlight() {
		t.Errorf("expected InFlight=true after Request")
	}
}

func TestSaveCoordinator_RequestParksWhenBusy(t *testing.T) {
	c := saveCoordinator{inFlight: true}
	first := cache.New()
	first.Repos["/a"] = repo.Repo{Path: "/a"}
	c, out := c.Request(first)
	if out != nil {
		t.Errorf("Request should park while in flight; got %v", out)
	}
	if c.pending != first {
		t.Errorf("expected first snapshot parked")
	}

	// A second request replaces the parked snapshot — coalesces older ones.
	second := cache.New()
	second.Repos["/b"] = repo.Repo{Path: "/b"}
	c, out = c.Request(second)
	if out != nil {
		t.Errorf("second Request should still park; got %v", out)
	}
	if c.pending != second {
		t.Errorf("expected pending to be replaced with newer snapshot")
	}
}

func TestSaveCoordinator_CompleteDispatchesPending(t *testing.T) {
	queued := cache.New()
	c := saveCoordinator{inFlight: true, pending: queued}
	c, next := c.Complete()
	if next != queued {
		t.Errorf("Complete should return the queued snapshot; got %v", next)
	}
	if !c.InFlight() {
		t.Errorf("expected InFlight=true while dispatching pending")
	}
	if c.pending != nil {
		t.Errorf("expected pending cleared after dispatch")
	}
}

func TestSaveCoordinator_CompleteEmptyQueue(t *testing.T) {
	c := saveCoordinator{inFlight: true}
	c, next := c.Complete()
	if next != nil {
		t.Errorf("Complete with empty queue should return nil; got %v", next)
	}
	if c.InFlight() {
		t.Errorf("expected InFlight=false after Complete with empty queue")
	}
}
