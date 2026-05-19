---
name: archetipo-spec
description: Creates the initial product backlog from a PRD, or appends new user stories to an existing one. Use whenever the user asks for a backlog, epics, user stories, or wants to add a single feature — even if the backlog does not yet exist.
---

# ARchetipo - Spec Skill

You are the public entry point for ARchetipo backlog and user-story work.

Your job is to understand whether the user needs to create the first backlog or extend an existing one, load only the references that matter for that case, and execute the correct flow without making the user choose between overlapping skills.

Treat routing as an internal implementation detail.

## Shared Runtime

Read `.archetipo/shared-runtime.md` for Language Policy, Assumptions and Questions, Conversation Rules, and File Output Rules.

## Core Principle

Keep the working context lean:
- Load this file first
- Load exactly one main flow reference at activation time
- Connector logic lives in the `archetipo` CLI; this skill never interprets a connector file

## Supported Modes

### `mode: bootstrap-backlog`

Use this mode when:
- the user asks to generate a backlog from an existing PRD or requirements artifact
- no backlog exists yet
- the user asks for the first epics or user stories of the project

In this mode:
1. Read this file
2. Read `./references/backlog-bootstrap-flow.md`
3. Use the PRD as the primary source and create the initial backlog

### `mode: extend-backlog`

Use this mode when:
- a backlog already exists
- the user asks to add, refine, split, or append user stories
- the user wants to extend the backlog without regenerating it from scratch

In this mode:
1. Read this file
2. Read `./references/story-extension-flow.md`
3. Use the existing backlog as the primary source and PRD/codebase as supporting context
4. Append or create only the requested items

## Config Loading & Connector Dispatch

1. Run `archetipo config` and parse the stdout JSON envelope. The `data` field is a `SetupInfo` object.
2. On failure, parse stderr as the JSON error envelope and branch on `error.code`.
3. This skill uses only these CLI operations:
   - `archetipo config`
   - `archetipo backlog show`
   - `archetipo story add --file <path|->`

Extract and keep available from `data`:
- `connector`
- `paths.prd`
- `paths.backlog`
- `paths.planning`
- `paths.mockups`
- `workflow.statuses`

## Backlog Discovery

Use this routine whenever the skill must decide whether it is extending an existing backlog or creating the first one.

Run `archetipo backlog show` and parse the JSON envelope. The CLI returns `data.items` (full Story objects) and `data.summary` with codes, last code, epics, and titles for the existing backlog.

If `data.summary.codes` is non-empty, use the existing stories as the source of truth for backlog extension.
If `data.summary.codes` is empty, treat the project as backlog-less and route to initial backlog creation.

## PRD Discovery

Use this routine whenever initial backlog creation needs a PRD or when story extension needs extra product context:

1. Try to read `{config.paths.prd}`
2. Only if that fails with file not found:
   - search markdown files in `docs/`
   - prefer files whose name or content indicates they are a PRD
3. Only if still not found:
   - search for `PRD*` files anywhere in the project

If a PRD is not found and the active flow needs one, ask the user for one of these:
- the file path
- the content pasted directly
- confirmation that they want to run product inception first

## Intent Routing

Use these routing rules before producing any substantive output.

1. Load this file
2. Run `archetipo config` and use `data.paths` / `data.workflow.statuses` as the project metadata source
3. Run backlog discovery
4. Decide the flow

Prefer `mode: bootstrap-backlog` when:
- the backlog does not exist
- the user explicitly asks to generate the backlog from a PRD or requirements
- the repository has a PRD but no backlog yet

Prefer `mode: extend-backlog` when:
- the backlog already exists
- the request is about one or more incremental stories, a new feature slice, a refinement, or a split

If a backlog already exists but the user explicitly asks to regenerate it from the PRD:
- ask for confirmation before overwriting or recreating the initial backlog

Do not expose mode names, routing decisions, or workflow labels in user-facing messages.

## Runtime Rules

- Ask clarifying questions only when critical information is missing and cannot be inferred responsibly
- Group clarifying questions in a single message when possible

## Output Boundaries

- Initial backlog creation belongs to this skill, not to `archetipo-inception`

## Story Template

Use this shape for every story, in both initial backlog generation and story extension:

```markdown
#### US-XXX: [Concise action-oriented title]

**Epic:** EP-XXX | **Priority:** HIGH | **Story Points:** N | **Status:** {config.workflow.statuses.todo}
**Blocked by:** -

**Story**
As [persona name or role],
I want [specific action or capability],
so that [concrete benefit tied to a PRD goal].

**Demonstrates**
After implementing this story, [describe what can be concretely observed or verified — e.g. for a feature: "the user can open the reports page, click Export, and download a CSV"; for a foundational story: "a developer can run the test suite and see all checks pass"]

**Acceptance Criteria**
- [ ] [Primary happy path]
- [ ] [Validation or error case]
- [ ] [Relevant edge case]
```
