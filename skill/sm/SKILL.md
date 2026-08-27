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
sm status --repo ~/.sm <producer-id>
sm scan --repo ~/.sm --json <producer-id>
```

`sm status` is the cross-layer preflight: it reports Producer root/HEAD/dirty state, canonical Skill presence, catalog HEAD/pending commit, and the exact next review command. Use `--json` when another tool consumes the state. Omit `<producer-id>` only for an explicitly requested fleet view.

Treat `new`, `updated`, `conflict`, and `invalid` as distinct facts. Do not publish through conflicts or invalid artifacts.
Omit `<producer-id>` only for an explicitly requested fleet audit.

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
Read its handoff as state: old/new artifact hashes, observed Producer HEAD and
dirty flag, changed catalog files, and the next review command. When
`pendingCommit=true`, `sm build` still reads the previous catalog HEAD; review
and commit before building.

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
sm replace-drifted --repo ~/.sm --evidence-output /path/to/evidence <consumer-id>
sm verify --repo ~/.sm <consumer-id>
sm verify --repo ~/.sm --closed <consumer-id>
sm verify --repo ~/.sm --closed --json <consumer-id>
sm exec --repo ~/.sm <consumer-id> -- <agent-arguments...>
```

`verify` requires every managed Skill and executable projection to be present;
it reports external Codex Skills without failing and attaches adapter proof
evidence in `--json` output. Use `verify --closed` when the persistent discovery
surface itself must equal the SSOT (Codex), or when Claude/Pi closed proofs must
hold. Use `sm exec` to derive and prove a closed execution profile without
deleting platform or plugin Skills.
Use `sm replace-drifted` only when `apply` refuses an sm-owned target whose
projection content hash has drifted; it preserves the old generation, writes
evidence, and verifies after activation.

If a Skill declares `executables` in its frontmatter, persistent consumers must
declare an `executablesTarget` directory already present on the Agent process's
`PATH`. Never install a second copy with the Producer's `make install` target.

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
