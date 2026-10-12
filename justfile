generate:
    sqlc generate

check-generated:
    sqlc diff

vet:
    go vet ./...

test:
    go test ./...

build goos="linux" goarch="amd64":
    CGO_ENABLED=0 GOOS={{goos}} GOARCH={{goarch}} go build -o bin/hermes-chat-{{goos}}-{{goarch}} ./cmd/hermes-chat

lint-scripts:
    shellcheck deploy/hermes-chat-upgrade

check: check-generated vet lint-scripts test
