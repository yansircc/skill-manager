.PHONY: build dashboard skill test

SKILL_SRC := skill/sm
SKILL_DIST := dist/skill

dashboard:
	npm install --prefix dashboard
	npm run build --prefix dashboard

build: dashboard
	go build .

skill:
	mkdir -p $(SKILL_DIST)
	rsync -a --delete $(SKILL_SRC)/ $(SKILL_DIST)/

test: dashboard
	go test ./...
