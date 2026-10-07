# syntax=docker/dockerfile:1
FROM node:22-alpine AS frontend-build
WORKDIR /app/frontend
COPY frontend/package.json frontend/package-lock.json* ./
RUN npm ci --prefer-offline
COPY frontend/ ./
ARG APP_VERSION=dev
ENV VITE_APP_VERSION=$APP_VERSION
RUN npm run build

# ---- Go build ----
FROM golang:1.27-alpine AS go-build
WORKDIR /app

# Dependencies first for layer caching
COPY go.mod go.sum ./
RUN go mod download

COPY . .
# Copy compiled frontend into the location the embed picks up (relative to cmd/server/)
COPY --from=frontend-build /app/frontend/dist ./cmd/server/frontend/dist

RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -tags prod \
    -ldflags="-s -w" \
    -o /out/wifi-diagnostics \
    ./cmd/server

# ---- Runtime image ----
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=go-build /out/wifi-diagnostics /wifi-diagnostics

USER nonroot:nonroot

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
    CMD ["/wifi-diagnostics", "-healthcheck"]

ENTRYPOINT ["/wifi-diagnostics"]
