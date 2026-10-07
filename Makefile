.PHONY: web build run dev test check

web:
	cd web && npm ci --no-audit --no-fund && npm run build

build: web
	CGO_ENABLED=0 go build -trimpath -o bin/portal ./cmd/portal

# Serve the built UI and API on http://localhost:3000.
run: build
	./bin/portal

# API on :3000 plus the Vite dev server (hot reload) on :5173.
dev:
	@test -d web/dist || (cd web && npm ci --no-audit --no-fund && npm run build)
	go run ./cmd/portal & cd web && npm run dev

test:
	go test -race ./...

check: test
	cd web && npm run typecheck
	go vet ./...
