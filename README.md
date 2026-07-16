# sm

`sm` compiles Agent skill projections from a Git source of truth.

The committed tree is authoritative:

```text
skillspace/
├── skills/
│   └── example/
│       └── SKILL.md
└── consumers/
    └── codex.global.json
```

A consumer is a direct authorization relation:

```json
{
  "adapter": "directory",
  "target": "/absolute/path/to/exclusive/discovery-root",
  "skills": ["example"]
}
```

Pi uses an ephemeral execution profile instead of a persistent target:

```json
{
  "adapter": "pi",
  "skills": ["example"]
}
```

Claude also uses an ephemeral execution profile:

```json
{
  "adapter": "claude",
  "skills": ["example"]
}
```

Codex uses its official global skills root as an exclusive target:

```json
{
  "adapter": "codex",
  "target": "~/.agents/skills",
  "skills": ["example"]
}
```

`directory` is the MVP adapter. It preserves each canonical skill directory exactly. A build reads only a Git commit, creates a read-only generation addressed by the commit, consumer, and compiler artifact, and `apply` atomically points the consumer target at that generation.

## Use

```sh
go install .

sm init ~/skills
sm scan --repo ~/skills ~/code ~/.claude/skills
sm adopt --repo ~/skills ~/some-project/my-skill
git -C ~/skills add skills consumers
git -C ~/skills commit -m 'Adopt my-skill'

sm build --repo ~/skills codex.global
sm apply --repo ~/skills codex.global
sm verify --repo ~/skills codex.global
sm exec --repo ~/skills codex.global -- exec "review this repository"

sm exec --repo ~/skills pi.global -- "review this repository"
sm exec --repo ~/skills claude.global -- "review this repository"
```

`adopt` moves the source directory into the SSOT. It deliberately refuses cross-filesystem adoption instead of leaving two editable copies.

`scan` is read-only. It recursively discovers candidate directories containing `SKILL.md`, excludes skills already inside the SSOT, and fails on duplicate IDs, catalog collisions, symlinks, special files, or nested `SKILL.md` packages. Discovery never grants canonical ownership; only `adopt` does.

`apply` refuses to replace a non-empty unmanaged target. The target must be absent, empty, or already point to an `sm` generation.

The target must be an exclusive discovery root configured in the Agent. The guarantee covers that root only. Agent built-ins or additional project-local discovery roots remain outside the guarantee until their adapter proves those surfaces can be disabled or controlled.

The `pi` adapter launches `pi --no-extensions --no-skills --skill <generation>`. Pi documents explicit `--skill` paths as additive even when discovery is disabled, so this execution profile exposes only the committed projection. `sm` rejects user-provided `--skill` and `--extension` arguments because they would expand that set.

The `claude` adapter compiles the generation as a native Claude plugin. It launches Claude with a consumer-stable isolated `CLAUDE_CONFIG_DIR`, no user/project setting sources, bundled skills disabled, and only the generated `--plugin-dir`. Before launch it scans the current repository, nested directories, the managed Claude directory, and the isolated profile; any other skill, command, or plugin source makes execution fail until it is adopted or removed.

Claude stores macOS Keychain credentials per `CLAUDE_CONFIG_DIR`. The first `sm exec` for a Claude consumer therefore requires logging in to that stable profile once, or supplying an official environment credential such as `CLAUDE_CODE_OAUTH_TOKEN`. Skill generation changes do not change the profile path or require another login.

The `codex` adapter verifies the real Codex `skills/list` projection through `codex app-server`. System skills are platform-owned. Any other enabled user, repository, admin, or plugin skill whose resolved path is outside the active generation makes verification fail. Those skills must be adopted into the SSOT or disabled at their original source; `sm` does not silently mask a second source of truth.

`sm exec` can close a Codex CLI profile without deleting installed plugins. It reads the live projection, derives temporary `skills.config` disables for every external non-system path, runs a second `skills/list`, and starts Codex only when the enabled set equals the active generation. User `--config`, `--profile`, and `--cd` overrides are rejected because they would invalidate that proof.
