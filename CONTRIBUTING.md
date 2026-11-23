# Contributing

Tracearr Bridge is a focused compatibility adapter for the Jellyfin API routes
used by Tracearr. Keep changes within that scope.

## Development setup

Requirements:

- Go 1.26 or newer.
- Docker only when building the release artifact locally.

Run the checks from the repository root:

```sh
make test
make vet
make build
```

Use `go test -race ./...` when changing concurrency, caches, pagination, or the
plugin lifecycle.

## Code guidelines

- Use Go formatting and keep the package buildable with `CGO_ENABLED=0`.
- Prefer explicit errors over partial empty success responses.
- Preserve Silo identifiers and never fabricate unavailable metadata.
- Bound network reads, retained collections, request bodies, and caches.
- Add tests for route behavior, malformed upstream data, authentication, and
  failure paths.
- Update `docs/compatibility.md` when a Tracearr route or data contract changes.

## Secrets

Never commit API keys, bridge tokens, Tracearr session data, or captured
production responses containing identifiable user data. Tests must use local
fixtures and synthetic credentials.

## Pull requests

Include:

- The problem being solved.
- The affected Jellyfin or Silo contract.
- Tests run.
- Any live-verification limits.
- Migration impact for Tracearr identifiers, history, or configuration.

Changes that alter identifiers, history semantics, or listener behavior require
an explicit changelog entry.

## Release changes

Do not update release tags or publish artifacts from a pull request. Follow
[RELEASING.md](RELEASING.md) after the change is merged.
