# @techreloaded/archetipo

Global CLI for the ARchetipo workflow.

## Install

```bash
npm i -g @techreloaded/archetipo
```

The package ships a small Node shim plus a native Go binary delivered as an
`optionalDependencies` per platform (darwin/linux/win32 × arm64/x64). npm will
install only the binary that matches the host.

## Bootstrap a project

```bash
cd my-project
archetipo init                # interactive: pick tools + connector
archetipo init --tool claude --connector file
```

`init` copies the ARchetipo skills (`archetipo-autopilot`, `archetipo-design`,
`archetipo-implement`, `archetipo-inception`, `archetipo-plan`,
`archetipo-spec`) under the tool-specific directory of the current project
(e.g. `.claude/skills/`) and creates `.archetipo/config.yaml` and
`.archetipo/shared-runtime.md`.

## Other commands

```bash
archetipo config              # connector handshake (auth + detect + metadata)
archetipo uninstall --tool claude
archetipo update              # npm i -g @techreloaded/archetipo@latest
archetipo update --check      # compare installed vs latest on the registry
archetipo --version
```

## Update notifications

A passive update notifier checks the npm registry once every 24 hours (cached
on disk) and prints a discreet banner on stderr when a newer version is
available. Disable with `ARCHETIPO_NO_UPDATE_NOTIFIER=1`.

## Documentation

Full docs and skill reference: https://github.com/techreloaded-ar/ARchetipo
