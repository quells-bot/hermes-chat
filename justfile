generate:
    sqlc generate

check-generated:
    sqlc diff

vet:
    go vet ./...

test:
    go test ./...

web-install:
    cd web && npm ci

web-build: web-install
    cd web && npm run build

web-check: web-install
    cd web && npm run check && npm test

web-dev:
    cd web && npm run dev

build goos="linux" goarch="amd64": web-build
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -o bin/hermes-chat-{{goos}}-{{goarch}} ./cmd/hermes-chat

lint-scripts:
    shellcheck deploy/hermes-chat-upgrade deploy/hermes-chat-admin

check: check-generated vet lint-scripts web-check test
