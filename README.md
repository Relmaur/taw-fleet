# taw-fleet

**Every TAW site on your Mac, in one terminal screen.**

`taw-fleet` finds the TAW themes in your Local by Flywheel sites without any setup. It shows each
one's status, taw/core version and git state, and opens the theme in your editor, the site in your
browser or the repo on GitHub.

> **Status: early development.** Only `taw-fleet version` exists so far. The plan is in the TAW
> umbrella at `docs/plans/taw-fleet.md`.

## Develop

```bash
brew install go golangci-lint
go test -race ./...
golangci-lint run
go run ./cmd/taw-fleet version
```

See [AGENTS.md](AGENTS.md) for the rules and
[ADR-0001](docs/adr/0001-go-tui-for-the-local-taw-fleet.md) for why it's built this way.
