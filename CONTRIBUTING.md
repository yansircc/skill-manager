# Contributing

## Development setup

Install Go 1.25 or newer, Node.js 22.12 or newer, npm, and Git.

```sh
git clone https://github.com/yansir/sm.git
cd sm
npm ci --prefix dashboard
```

## Change boundaries

- Keep the Git catalog as the only editable truth.
- Derive generations and Agent projections from committed catalog state.
- Keep Producer build outputs outside the catalog.
- Do not add fallback discovery paths or duplicate authorization state.
- Treat Producer commands and skill contents as trusted code, not sandboxed input.

## Verification

Run the complete local gate before submitting a pull request:

```sh
npm run build --prefix dashboard
git diff --exit-code -- dashboard/dist
test -z "$(gofmt -l .)"
go vet ./...
go test ./...
go build ./...
```

Frontend changes must include the regenerated `dashboard/dist` assets. Add focused tests for behavior changes and keep fixtures free of credentials or machine-specific paths.

## Pull requests

Use a focused title, explain the invariant being changed, and include verification evidence. Avoid compatibility shims unless the failure model and removal condition are explicit.
