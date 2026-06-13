# archetipo CLI

Deterministic Go implementation of the ARchetipo workflow operations. Replaces markdown-described connector behavior with one binary that performs the public backlog, story, task, PRD, and board operations directly.

## Build

```bash
cd cli
go build ./cmd/archetipo
```

The output binary `archetipo` reads `.archetipo/config.yaml` from the project root (or any ancestor) to choose the connector (`file`, `github` or `jira`) and execute the requested sub-command.

With the `jira` connector only `jira.base_url` (or `JIRA_BASE_URL`) plus the `JIRA_EMAIL`/`JIRA_API_TOKEN` env vars are required up front: on first run the CLI looks for a Jira project named after the project directory, creates it when missing (project creation needs the *Administer Jira* permission — without it the CLI reports the error and you can set `jira.project_key` manually instead), discovers how the canonical workflow statuses map onto the project's Jira statuses, and writes `project_key` and `status_map` back into `config.yaml` where they can be corrected by hand. Canonical statuses the Jira workflow lacks (the standard templates ship without a REVIEW-like status) are provisioned automatically: the CLI creates the status and adds it to the project's workflow, falling back to a manual-fix hint when the token lacks the required permission.

To build all release binaries locally from the repository root:

```bash
npm run build:cli
```

## Layout

```
cmd/archetipo/        # entry point
internal/
  cli/                # cobra sub-commands (one file per operation)
  connector/          # interface, registry, three implementations + inmemory ref
    filefs/           # markdown + HTML-comment markers
    github/           # gh CLI shell-out + GraphQL aliased mutations
    jira/             # Jira Cloud REST API v3 over an injectable HTTP Doer
    inmemory/         # reference impl used by the conformance suite
    conformance/      # behavioural test suite shared by every implementation
  config/             # .archetipo/config.yaml loader
  domain/             # canonical data types
  iox/                # JSON envelope on stdin/stdout/stderr + typed errors
  version/            # injected via -ldflags at release time
```

## Tests

```bash
go test ./...
```

The conformance suite runs against `filefs` and `inmemory`. The `github` connector is exercised with a mock `gh` runner; live smoke tests are gated behind `ARCHETIPO_E2E_GH=1` and need a sandbox repo with `gh` authenticated. The `jira` connector is exercised against an in-memory fake Jira backend that implements the REST endpoints it calls (see `internal/connector/jira/jira_test.go`).

## Distribution

Tags `vX.Y.Z` produce a single bare binary per platform via GoReleaser. Release assets are named `archetipo-<os>-<arch>` for macOS/Linux and `archetipo-windows-<arch>.exe` for Windows. These binaries are then bundled by `scripts/build-npm.mjs` into the per-platform npm packages (`@techreloaded/archetipo-<platform>-<arch>`) that `@techreloaded/archetipo` pulls in as `optionalDependencies`.
