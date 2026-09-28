.PHONY: check build run audit
check:
	go vet ./...
	go test ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed"
	@command -v govulncheck >/dev/null && govulncheck ./... || echo "govulncheck not installed"
build:
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/sentinela ./cmd/sentinela
run:
	go run ./cmd/sentinela
audit:
	./scripts/camera-audit.sh $(CAMERA)
