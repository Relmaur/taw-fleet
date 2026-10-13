# ADR-0002: taw-fleet serves its commands over MCP (`taw-fleet mcp`, stdio), as a thin layer over the same actions

## Status

Accepted (2026-10-12). The owner approved the direction ("Yes! Let's do it!", umbrella plan
`docs/plans/taw-fleet-mcp-and-operations.md`).

## Context

Claude sessions already run the fleet: they call `taw-fleet … --json` and the site skills
(`resolve-comments`, `perf-audit`) read `taw-fleet show|comments <site> --json`. Three problems:

1. **Untyped and parsed.** Each session learns the command lines and the JSON shapes from docs.
2. **Writes are raw shell.** A merge is `gh pr merge`, a force-push is `git push --force-with-lease`.
   Permission layers can only see a shell line: on 2026-10-10 the auto-mode classifier blocked every
   merge and a client theme's mirror realign until the owner approved each by hand. Asking first, which
   the dashboard does for every write, isn't carried over.
3. **No shell, no fleet.** Claude Desktop, Cursor and claude.ai can't run commands on this Mac.

The options:

- **(a) Keep the CLI only**, and document it better.
- **(b) An MCP server inside taw-fleet.**
- **(c) A separate MCP package** (Node or PHP) calling the CLI.
- **(d) An MCP server in taw/core**, per theme.

## Decision

1. **`taw-fleet mcp`** serves the Model Context Protocol on stdio, in the same binary, with the
   official Go SDK (`github.com/modelcontextprotocol/go-sdk`). Clients add it once:
   `claude mcp add taw-fleet -- taw-fleet mcp`.
2. **Thin layer, same code.** Every tool calls the packages the dashboard and CLI use (`scan`,
   `doctor`, `actions`, `github`, `live`, `bugsmash`, `tawcmd`). A tool's result is the CLI's
   `--json` for the same thing. No logic lives only in `internal/mcp`.
3. **One tool, one human path** (umbrella ADR-0004). Each tool's description names its CLI command
   and dashboard key. `taw-fleet mcp tools` prints the list.
4. **Read-only first** (v1.18.0): sites, site, doctor, pull requests, comments, production, and a
   theme's `taw` limited to read-only commands. Annotated `readOnlyHint`.
5. **Writes ask first** (v1.19.0), as the dashboard does:
   - Called without `confirm`, a write tool returns the dashboard's question and a one-time token.
   - Called again with that token, it runs.
   - Tools that reach production are annotated `destructiveHint`.
   - The client's own per-tool approval sits on top.
6. **Theme commands go through `taw`**, so they run with the site's PHP and socket. There's no
   second, per-theme MCP server.

## Trade-offs

- **(a) lost:** it keeps problems 1–3. The CLI stays, and MCP is built on it.
- **(c) lost:** a second runtime and package to ship and keep in step, for a layer this thin.
- **(d) lost:** one server per theme, with nothing to see across sites. The fleet-level server reaches
  each theme through `taw` anyway.
- **Given up:** the SDK becomes a dependency of the binary (pure Go, no CGO, so it fits ADR-0001's
  one static binary). Tool names and result shapes become a contract, like the `--json` output
  already is.

## Consequences

- `internal/mcp` and the `mcp` command exist. Tests drive the server in-process (the SDK's in-memory
  transport), against the usual test fakes, never the real `~/Library` or the network.
- A new CLI or dashboard action that agents should use also gets a tool, or a line in the plan
  saying why not.
- The `taw` tool keeps an allowlist of read-only theme commands until the confirm flow (decision 5)
  covers the rest.
- Not done yet (YAGNI):
  - an HTTP transport;
  - MCP resources beyond prompts;
  - authentication. stdio runs as the user, like the CLI.
