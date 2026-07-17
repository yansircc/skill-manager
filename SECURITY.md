# Security Policy

## Supported versions

Security fixes are applied to the latest release and the default branch.

## Reporting a vulnerability

Do not open a public issue for a suspected vulnerability. Use [GitHub private vulnerability reporting](https://github.com/yansircc/skill-manager/security/advisories/new) and include the affected version, impact, reproduction steps, and any proposed mitigation.

## Security model

`sm` is a local skill compiler and activator. It does not sandbox Producers, skills, or Agent processes.

- Producer `build.argv` commands execute as the current user and must be trusted.
- Published skills may contain executable files and must be reviewed like code.
- Consumer declarations restrict which skills enter a projection; they are not an operating-system security boundary.
- The Dashboard has a mutation API without authentication. It is restricted to loopback addresses and must not be exposed through a reverse proxy, tunnel, or port forward.
- Immutable generations are derived from committed Git trees. Unexpected working-tree changes should be reviewed before committing or publishing.
