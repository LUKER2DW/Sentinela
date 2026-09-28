.PHONY: check web build run test docker audit
check:
	go vet ./...
	go test ./...
	@command -v staticcheck >/dev/null && staticcheck ./... || echo "staticcheck not installed"
	@command -v govulncheck >/dev/null && govulncheck ./... || echo "govulncheck not installed"
	cd web && npm run lint && npm test
web:
	cd web && npm ci && npm run build
build: web
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/sentinela ./cmd/sentinela
run:
	go run ./cmd/sentinela serve -config deploy/config.example.yaml
test:
	go test ./... && cd web && npm test
docker:
	docker build -t sentinela:local .
audit:
	./scripts/camera-audit.sh $(CAMERA)
