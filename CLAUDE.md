# CLAUDE.md — taw-fleet

Full reference: **`AGENTS.md`** in this folder. Also read the umbrella's `AGENTS.md` and
`docs/STATE.md` (one level up), and the plan at umbrella `docs/plans/taw-fleet.md`.

- Go + Bubble Tea v2 (`charm.land/*/v2`) + cobra. One static binary, no CGO.
- Paths through `paths.Paths`; commands through `exec.Runner`; argument lists only, never shell
  strings.
- Tests never read the real `~/Library` or the network.
- `go test -race ./...` + `golangci-lint run` before committing.
