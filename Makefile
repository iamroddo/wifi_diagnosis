.PHONY: build build-dev test lint frontend frontend-dev frontend-install \
        docker-build docker-build-multiarch docker-run docker-run-dev \
        deploy deploy-image deploy-files deploy-start deploy-restart deploy-status deploy-logs \
        clean

# Load .env so NAS_IP, CONTAINER_IP etc. are available as make variables.
# -include silently skips the file if it doesn't exist (e.g. in CI).
-include .env
export

NAS_USER      ?= admin
NAS_DEPLOY_DIR ?= /volume1/docker/wifi-diagnostics
NAS            := $(NAS_USER)@$(NAS_IP)
NAS_DOCKER     := sudo /var/packages/ContainerManager/target/usr/bin/docker
NAS_COMPOSE    := sudo /var/packages/ContainerManager/target/usr/bin/docker-compose

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
	@VERSION=$$(git rev-parse --short HEAD) && DATE=$$(date +%Y-%m-%d) && \
	  sed -i '' "s|^\*\*Version:\*\* .*|\*\*Version:\*\* $${DATE}-$${VERSION}|" README.md && \
	  echo "version: $${DATE}-$${VERSION}"

frontend-dev:
	cd frontend && npm ci && npm run dev

frontend-install:
	cd frontend && npm ci

# ---- Docker (local) ----
docker-build:
	docker build --platform linux/amd64 \
		--build-arg APP_VERSION=$(shell date +%Y-%m-%d)-$(shell git rev-parse --short HEAD) \
		-t wifi-diagnostics:latest .

docker-build-multiarch:
	docker buildx build --platform linux/amd64,linux/arm64 -t wifi-diagnostics:latest --push .

docker-run:
	docker run --rm -p 8080:8080 \
		--env-file .env \
		-v $(PWD)/data:/app/data \
		wifi-diagnostics:latest

docker-run-dev:
	go run ./cmd/server

# ---- Deploy to Synology ----

## Build the linux/amd64 image, save it, and stream it directly to the NAS.
deploy-image: frontend
	$(eval APP_VERSION := $(shell date +%Y-%m-%d)-$(shell git rev-parse --short HEAD))
	@echo "==> Building image for linux/amd64 (version: $(APP_VERSION))..."
	docker build --no-cache --platform linux/amd64 --load \
		--build-arg APP_VERSION=$(APP_VERSION) \
		-t wifi-diagnostics:latest .
	@echo "==> Streaming image to $(NAS):$(NAS_DEPLOY_DIR)..."
	docker save wifi-diagnostics:latest | gzip | ssh $(NAS) "cat > /tmp/wifi-diagnostics.tar.gz"
	@echo "==> Loading image on NAS..."
	ssh $(NAS) "$(NAS_DOCKER) load < /tmp/wifi-diagnostics.tar.gz && rm /tmp/wifi-diagnostics.tar.gz"

## Copy docker-compose.yml and .env to the NAS deploy directory.
deploy-files:
	@echo "==> Ensuring deploy directory exists on NAS..."
	ssh $(NAS) "mkdir -p $(NAS_DEPLOY_DIR)/data"
	@echo "==> Copying compose file and .env..."
	scp -O docker-compose.yml .env $(NAS):$(NAS_DEPLOY_DIR)/

## Start the service on the NAS (no-op if already running).
deploy-start:
	ssh $(NAS) "cd $(NAS_DEPLOY_DIR) && $(NAS_COMPOSE) up -d"

## Pull a fresh image, copy config, and restart the service — full redeploy.
deploy: deploy-image deploy-files deploy-restart

## Restart (or start) the service without rebuilding the image.
deploy-restart:
	ssh $(NAS) "cd $(NAS_DEPLOY_DIR) && \
	  $(NAS_COMPOSE) down 2>/dev/null; \
	  $(NAS_DOCKER) rm -f wifi-diagnostics 2>/dev/null; \
	  $(NAS_COMPOSE) up -d"

## Show running container status on the NAS.
deploy-status:
	ssh $(NAS) "cd $(NAS_DEPLOY_DIR) && $(NAS_COMPOSE) ps"

## Tail live logs from the NAS (Ctrl-C to stop).
deploy-logs:
	ssh $(NAS) "cd $(NAS_DEPLOY_DIR) && $(NAS_COMPOSE) logs -f --tail=100"

# ---- Cleanup ----
clean:
	rm -f wifi-diagnostics
	rm -rf frontend/dist
	rm -rf data/
