package repo_test

import (
	"path/filepath"
	"testing"

	"github.com/sethdeckard/atlas/internal/repo"
)

func TestDisplayPath(t *testing.T) {
	cases := []struct {
		name string
		root string
		repo repo.Repo
		want string
	}{
		{
			name: "root-level shows just name",
			root: "/r",
			repo: repo.Repo{Name: "all-ideas", Path: "/r/all-ideas"},
			want: "all-ideas",
		},
		{
			name: "subdir shows top_dir/name",
			root: "/r",
			repo: repo.Repo{Name: "atlas", Path: "/r/go/atlas"},
			want: "go/atlas",
		},
		{
			name: "deep nesting shows immediate parent",
			root: "/r",
			repo: repo.Repo{Name: "api", Path: "/r/work/sub/deep/api"},
			want: "deep/api",
		},
		{
			name: "high-up root surfaces actual neighborhood, not top dir",
			root: "/home/u",
			repo: repo.Repo{Name: "atlas", Path: "/home/u/projects/go/atlas"},
			want: "go/atlas",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repo.DisplayPath(c.root, c.repo)
			if got != c.want {
				t.Errorf("DisplayPath(%q, %+v) = %q; want %q", c.root, c.repo, got, c.want)
			}
		})
	}
}

func TestPathUnderRoot(t *testing.T) {
	sep := string(filepath.Separator)
	cases := []struct {
		name string
		path string
		root string
		want bool
	}{
		{"identical", "/root", "/root", true},
		{"direct child", "/root" + sep + "a", "/root", true},
		{"nested child", "/root" + sep + "a" + sep + "b", "/root", true},
		{"unrelated", "/elsewhere", "/root", false},
		// Sibling-prefix bug: /rootless starts with "/root" but is
		// not under /root because the separator-anchored prefix is
		// "/root/", not "/root".
		{"sibling-prefix", "/rootless", "/root", false},
		{"sibling-prefix nested", "/rootless/x", "/root", false},
		{"empty path", "", "/root", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := repo.PathUnderRoot(c.path, c.root)
			if got != c.want {
				t.Errorf("PathUnderRoot(%q, %q) = %v; want %v", c.path, c.root, got, c.want)
			}
		})
	}
}
