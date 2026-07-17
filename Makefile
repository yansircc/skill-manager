.PHONY: build dashboard skill test verify

SKILL_SRC := skill/sm
SKILL_DIST := dist/skill
VERSION ?= $(shell git describe --tags --always --dirty)

dashboard:
	npm ci --prefix dashboard
	npm run build --prefix dashboard

build: dashboard
	go build -trimpath -ldflags "-X main.version=$(VERSION)" .

skill:
	mkdir -p $(SKILL_DIST)
	rsync -a --delete $(SKILL_SRC)/ $(SKILL_DIST)/

test: dashboard
	go test ./...

verify: dashboard
	test -z "$$(gofmt -l .)"
	go vet ./...
	go test ./...
	go build ./...
