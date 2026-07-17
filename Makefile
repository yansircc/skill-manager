.PHONY: build dashboard test

dashboard:
	npm install --prefix dashboard
	npm run build --prefix dashboard

build: dashboard
	go build .

test: dashboard
	go test ./...
