# Domain Docs

## Before exploring

- Read `CONTEXT.md` at the repository root when it exists.
- Read ADRs in `docs/adr/` that affect the area being changed.
- If either location does not exist, proceed silently. Domain files are created lazily when terminology or durable decisions are resolved.

## Use the glossary vocabulary

Use the terms defined in `CONTEXT.md` in issue titles, plans, tests, and implementation discussions. Avoid synonyms explicitly rejected by the glossary.

If a required concept is missing, reconsider whether new terminology is necessary or record the gap for a domain-modeling session.

## Respect ADRs

Surface any conflict with an existing ADR explicitly. Do not silently override an accepted decision.

## Layout

This is a single-context repository:

```text
/
├── CONTEXT.md
└── docs/adr/
```
