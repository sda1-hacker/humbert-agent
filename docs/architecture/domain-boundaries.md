# Humbert Domain Boundaries

This document records the ownership rules introduced by the 2026-09 refactor. New code should follow these boundaries even while legacy fields remain for data compatibility.

## Agent

Agent owns reusable assistant identity and capabilities:

- name / instruction
- default model
- enabled Skills
- enabled built-in tools and MCP selections
- sandbox/security overrides

Agent does **not** own the active project workspace in new code. The legacy Agent workspace fields are retained only so existing installations can be migrated without moving user files.

Agent writes should use narrow commands (`UpdateProfile`, `SetModel`, `SetSkills`, `UpdateSecurity`) instead of a read-modify-write of the entire Agent record.

## Project

Project is a first-class domain object and owns work context:

- project name
- Agent reference
- workspace mode
- workspace path

The desktop UI may expose a Project + Agent aggregate DTO for convenience, but persistence and writes remain split by domain ownership.

On upgrade, every legacy Agent receives a same-ID Project. This preserves the existing physical session/workspace layout while establishing a migration path to multiple Projects per Agent later.

## Workspace

Workspace is resolved from Project configuration. Runtime code must resolve:

`Session -> Project -> Workspace`

and must not treat Agent workspace fields as authoritative.

## Session

Session records both `ProjectID` and `AgentID`.

- `ProjectID` determines work context/workspace.
- `AgentID` determines assistant identity and capabilities.

Schema-v1 sessions that do not contain `project_id` are read as `ProjectID == AgentID`. The compatibility mapping is intentionally non-destructive.

The physical session path remains under the Agent directory for now so this refactor does not move historical JSONL data. Storage layout can be migrated independently in a later version.

## Runtime

A Runtime turn freezes an immutable snapshot from:

`Session + Project + Agent + Model + Tools + Skills + MCP + Sandbox + Context`

Changing Project or Agent configuration affects future turns only.

## Chat input and attachments

User input is structured as text plus zero or more attachments. Attachment binary data is stored in a per-session `attachments/` sidecar directory. Transcript JSONL stores only stable attachment metadata/reference IDs.

Before a provider call, runtime hydrates those references into Eino multimodal `UserInputMultiContent`. Base64 payloads therefore do not pollute the transcript, memory, or context-compaction records.

## Compatibility rule

Compatibility code may read legacy fields, but new features must not create new dependencies on them. In particular:

- do not add new callers of full `UpdateAgent` for partial edits;
- do not resolve Workspace from Agent in Runtime;
- do not assume `ProjectID == AgentID` even though migration currently creates that relationship by default.
