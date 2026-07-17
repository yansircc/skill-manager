## Invariant

What ownership or projection invariant does this change preserve or establish?

## Change

Describe the minimal implementation boundary.

## Verification

- [ ] `npm run build --prefix dashboard`
- [ ] `git diff --exit-code -- dashboard/dist`
- [ ] `test -z "$(gofmt -l .)"`
- [ ] `go vet ./...`
- [ ] `go test ./...`
- [ ] `go build ./cmd/sm`
