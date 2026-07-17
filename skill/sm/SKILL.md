---
name: sm
description: "Manage the local SM skill registry, Producer-owned skill artifacts, Agent grants, immutable projections, and the Svelte dashboard. Use when an agent needs to inspect, add, generate, update, publish, authorize, build, apply, verify, or troubleshoot skills governed by ~/.sm for Codex, Claude, or Pi, or when the user asks to open or show the skill-management page so they can inspect or operate it themselves."
---

# SM

Treat `~/.sm` as the only editable registry and committed Git state as the only projection input.

```text
Producer repo -> external artifact -> ~/.sm/skills -> consumer generation -> Agent
```

## Preserve the boundaries

- Edit a generated skill in its Producer repo, then run `sm update`; never edit its canonical copy.
- Edit an unowned skill only in `~/.sm/skills/<id>`.
- Keep Producer repos unaware of `sm`, `~/.sm`, and Agent discovery paths. `make skill` must only create an external artifact.
- Define generated ownership once in `~/.sm/producers/<id>.json`. One Skill ID has exactly one Producer.
- Build consumer projections only from committed `~/.sm` state. Review and commit CLI-produced catalog changes before building.
- Do not restore the removed arbitrary-root `scan`, `adopt`, or `publish --id <path>` workflows.

## Inspect before changing

```sh
git -C ~/.sm status --short
sm producers --repo ~/.sm
sm scan --repo ~/.sm --json
```

Treat `new`, `updated`, `conflict`, and `invalid` as distinct facts. Do not publish through conflicts or invalid artifacts.

## Relocate a Producer

When a Producer repository moves, update its locator explicitly:

```sh
sm producer relocate --repo ~/.sm <producer-id> <new-root>
```

Require a clean SSOT. The command runs the existing build in `new-root`, validates the complete declared Skill set, and commits only `producers/<id>.json`. It does not publish artifacts or rebuild Agent generations. Run `sm update` separately only when the artifact should change.

Do not search for a replacement checkout by repository or directory name. Multiple clones and worktrees make inferred relocation ambiguous.

## Update generated skills

Update only the requested Producer unless the user explicitly requests a fleet update:

```sh
sm update --repo ~/.sm <producer-id>
git -C ~/.sm diff --stat
git -C ~/.sm add producers skills consumers .gitignore
git -C ~/.sm commit -m "Update <producer-id> skill artifact"
```

`sm update` is exactly `produce -> scan -> atomic publish`. A failure must leave the whole catalog unchanged.

## Manage Agent access

When the user asks to see or operate the interface, open it directly:

```sh
sm open --repo ~/.sm
```

This reuses a matching running Dashboard or opens a new local Dashboard and keeps its server in the foreground. Use `sm dashboard` only when serving without opening a browser.

Use the dashboard for grant toggles and source updates:

```sh
sm dashboard --repo ~/.sm
```

The dashboard commits grant changes, rebuilds the affected projection, and proves the managed execution closure. Do not edit a second authorization list outside `~/.sm/consumers`.

For CLI projection work:

```sh
sm build --repo ~/.sm <consumer-id>
sm apply --repo ~/.sm <consumer-id>
sm verify --repo ~/.sm <consumer-id>
sm exec --repo ~/.sm <consumer-id> -- <agent-arguments...>
```

Codex may have platform or plugin skills outside the persistent target. In that case, use `sm exec` to derive and verify the closed execution profile; do not delete external plugins merely to make persistent `verify` pass.

## Add a Producer

Prefer the dashboard. A registry entry has this contract:

```json
{
  "root": "/absolute/path/to/repo",
  "note": "Optional explanation shown in the Dashboard list",
  "build": { "argv": ["make", "skill"] },
  "outputs": [{ "path": "dist/skill" }],
  "skills": ["stable-skill-id"]
}
```

Require each emitted `SKILL.md` frontmatter `name` to equal its declared Skill ID. A Producer that emits multiple skills owns and publishes them as one transaction.
