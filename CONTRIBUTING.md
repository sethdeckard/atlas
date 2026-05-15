# Contributing to atlas

## Getting Started

```
git clone https://github.com/sethdeckard/atlas.git
cd atlas
make build
```

## Development

- **Build:** `make build` (or `go build ./cmd/atlas`)
- **Test:** `make test`
- **Lint:** `make lint` (requires `golangci-lint`)

## Code Style

- Format with `gofmt` / `goimports`
- Wrap errors with context: `fmt.Errorf("context: %w", err)`
- No `log.Fatal` in library code (`internal/` packages) — only `cmd/atlas/main.go` may exit the process

## Commit Messages

Subject lines: imperative mood, capitalized, ≤50 chars, no trailing
period. Body wrapped at 72 chars. The `commit-msg` hook enforces these
rules — activate it once after cloning:

```
make hooks
```

## Pull Requests

- Submit PRs against `main`
- Include a description of what changed and why
- Ensure `make test` passes

## Reporting Issues

Use [GitHub Issues](https://github.com/sethdeckard/atlas/issues) to report bugs or request features.
