# Humbert Agent Refactor Report

## Scope

This refactor intentionally focuses on three product/architecture priorities:

1. Replace accidental full-Agent updates with narrow domain commands.
2. Complete the chat basics: safe Markdown rendering, code copy, links, tables, and durable image/file attachments.
3. Establish explicit Agent / Project / Workspace / Session ownership without destructively migrating existing user data.

## Agent update contract

New code should use narrow Agent commands:

- `UpdateProfile`
- `SetModel`
- `SetSkills`
- `UpdateSecurity`

The desktop model switcher now calls only `SetAgentModel`. It no longer reconstructs and submits the complete Agent DTO, so changing a model cannot silently overwrite Workspace, Sandbox, or Skills.

The legacy full `UpdateAgent` service method remains temporarily for compatibility, but its Sandbox update now has explicit configured/preserve semantics. New UI code should not add new full-update callers.

## Project / Agent / Workspace / Session

A first-class `internal/projects` domain now exists.

- **Agent** owns identity/instruction, default model, Skills, built-in tools/MCP selections, and security overrides.
- **Project** owns the Workspace configuration and references an Agent.
- **Session** records both `ProjectID` and `AgentID`.
- **Runtime** resolves Workspace through `Session -> Project -> Workspace`, and Agent capabilities through `Session -> Agent`.

On upgrade, legacy Agents receive a same-ID Project. Old Session config schema v1 is read as `ProjectID == AgentID`. Existing JSONL/session directories are not moved.

The physical session layout still remains under the Agent directory as a compatibility detail. New application code must not infer domain ownership from that storage path.

See `docs/architecture/domain-boundaries.md` for the rules new code should follow.

## Chat basics

Assistant messages now use a structured Vue Markdown renderer instead of plain `white-space: pre-wrap` text. It supports:

- headings and paragraphs
- ordered/unordered lists
- block quotes
- fenced code blocks
- code copy
- inline code
- links with protocol filtering
- tables
- common inline emphasis
- safe Markdown images

The renderer does not use `v-html`, so model text is not injected directly as DOM HTML.

## Attachments

User input is now structured as text plus zero or more attachments.

Limits enforced by both UI and backend:

- maximum 8 attachments per message
- maximum 12 MiB per attachment
- maximum 24 MiB total per message

Attachment bytes are stored under each Session's `attachments/` sidecar directory. Transcript JSONL contains only attachment metadata and an internal reference ID; Base64 payloads are hydrated into Eino `UserInputMultiContent` only immediately before provider execution.

This keeps large binary data out of JSONL, memory summaries, and context-compaction records while allowing historical messages to restore previews/downloads.

## Compatibility

This refactor is deliberately incremental:

- legacy Agent Workspace fields remain readable only for migration;
- old Session schema v1 remains readable;
- the frontend store is still exported as `useAgentStore()` as a compatibility facade, although its list is now Project-based;
- the full `AgentService.UpdateAgent` method remains for old bindings/callers but is no longer used by the new desktop Project/model flows.

## Validation performed in this environment

Completed:

- `gofmt` on changed/new Go sources
- `git diff --check`
- `node --check` on all frontend JavaScript files
- structural checks on changed Vue SFC files
- Markdown feature/safety smoke tests
- static search confirming the frontend has no remaining `updateAgent(...)` caller

Not completed here:

- `go test ./...`
- `go vet ./...`
- Vue/Vite production build

The execution environment only has Go 1.23.2 while this project declares Go 1.26.1, and it cannot download the required toolchain/dependencies. The frontend dependency installation is also unavailable in this environment, so Vite is not present.

## Recommended local verification

With Go 1.26.1 and Node/npm available:

```bash
go test ./...
go vet ./...

cd frontend
npm ci
npm run build
cd ..

wails3 dev -config ./build/config.yml -port 9245
```

If Wails bindings are stale after the service additions, regenerate them with the Wails v3 binding-generation command used by your local toolchain before starting the desktop app.
