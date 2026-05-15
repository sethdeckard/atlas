package repo_test

import (
	"testing"
	"time"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestClassifyActivity_Boundaries(t *testing.T) {
	now := time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)
	day := 24 * time.Hour
	mk := func(d time.Duration) *time.Time {
		t := now.Add(-d)
		return &t
	}

	cases := []struct {
		name      string
		last      *time.Time
		staleDays int
		want      string
	}{
		{"nil", nil, 60, "empty"},
		{"13d-staleDays60", mk(13 * day), 60, "recent"},
		{"15d-staleDays60", mk(15 * day), 60, "active"},
		{"59d-staleDays60", mk(59 * day), 60, "active"},
		{"61d-staleDays60", mk(61 * day), 60, "cold"},
		{"364d-staleDays60", mk(364 * day), 60, "cold"},
		{"366d-staleDays60", mk(366 * day), 60, "dormant"},

		// staleDays=90: the active/cold boundary moves to 90d. The 14d
		// floor and the 365d ceiling stay fixed.
		{"15d-staleDays90", mk(15 * day), 90, "active"},
		{"91d-staleDays90", mk(91 * day), 90, "cold"},
		{"15d-staleDays180", mk(15 * day), 180, "active"},
		{"181d-staleDays180", mk(181 * day), 180, "cold"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := repo.ClassifyActivity(tc.last, now, tc.staleDays)
			if got != tc.want {
				t.Errorf("got %q; want %q", got, tc.want)
			}
		})
	}
}
