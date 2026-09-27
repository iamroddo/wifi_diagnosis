.PHONY: build test lint frontend docker-build docker-run clean

# ---- Go ----
build: frontend
	go build -o wifi-diagnostics ./cmd/server

build-dev:
	go build -o wifi-diagnostics ./cmd/server

test:
	go test ./...

lint:
	go vet ./...
	@which golangci-lint > /dev/null 2>&1 && golangci-lint run || echo "golangci-lint not installed, skipping"

# ---- Frontend ----
frontend:
	cd frontend && npm ci && npm run build

frontend-dev:
	cd frontend && npm ci && npm run dev

frontend-install:
	cd frontend && npm ci

# ---- Docker ----
docker-build:
	docker build --platform linux/amd64 -t wifi-diagnostics:latest .

docker-build-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 -t wifi-diagnostics:latest --push .

docker-run:
	docker run --rm -p 8080:8080 \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		wifi-diagnostics:latest

docker-run-dev:
	go run ./cmd/server

# ---- Cleanup ----
clean:
	rm -f wifi-diagnostics
	rm -rf frontend/dist
	rm -rf data/
