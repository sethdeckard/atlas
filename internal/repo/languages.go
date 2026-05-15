package repo

import (
	"os"
	"path/filepath"
)

// languageManifests maps a language label to the manifest filenames /
// glob patterns that identify it. Entries are tried in the order they
// appear in `languageOrder` below — the first match wins as the primary
// language; subsequent matches still append, so a polyglot like a Go
// service that ships a Dockerfile gets `["go", "docker"]`.
//
// Globs are resolved with filepath.Glob relative to the worktree root.
// Plain names use os.Stat for a fast exact-name check.
var languageManifests = map[string][]string{
	"go":     {"go.mod"},
	"rust":   {"Cargo.toml"},
	"node":   {"package.json"},
	"python": {"pyproject.toml", "requirements.txt", "setup.py"},
	"ruby":   {"Gemfile"},
	"java":   {"pom.xml", "build.gradle", "build.gradle.kts"},
	"swift":  {"Package.swift"},
	"php":    {"composer.json"},
	"elixir": {"mix.exs"},
	"dotnet": {"*.csproj", "*.fsproj"},
	"docker": {"Dockerfile", "compose.yaml", "compose.yml"},
}

// languageOrder is the precedence order for primary-language assignment.
// "Real" build manifests (go.mod, Cargo.toml, package.json) outrank
// supporting manifests (Dockerfile, docker-compose) so a polyglot's
// primary language matches what a developer would call it.
var languageOrder = []string{
	"go",
	"rust",
	"node",
	"python",
	"ruby",
	"java",
	"swift",
	"php",
	"elixir",
	"dotnet",
	"docker",
}

// DetectLanguages walks the worktree root (no recursion) and returns the
// labels of every manifest that matches, in precedence order. An empty
// slice is returned when no manifests match (or for bare repos that
// have no worktree).
func DetectLanguages(worktree string) []string {
	if worktree == "" {
		return nil
	}
	out := make([]string, 0, 2)
	for _, lang := range languageOrder {
		patterns := languageManifests[lang]
		if matchAny(worktree, patterns) {
			out = append(out, lang)
		}
	}
	return out
}

func matchAny(worktree string, patterns []string) bool {
	for _, p := range patterns {
		if containsGlobMeta(p) {
			matches, err := filepath.Glob(filepath.Join(worktree, p))
			if err == nil && len(matches) > 0 {
				return true
			}
			continue
		}
		if _, err := os.Stat(filepath.Join(worktree, p)); err == nil {
			return true
		}
	}
	return false
}

// containsGlobMeta reports whether p has any filepath.Glob metacharacter
// — used to decide between a Stat (cheap exact match) and a Glob (slower
// directory scan).
func containsGlobMeta(p string) bool {
	for i := 0; i < len(p); i++ {
		switch p[i] {
		case '*', '?', '[':
			return true
		}
	}
	return false
}
