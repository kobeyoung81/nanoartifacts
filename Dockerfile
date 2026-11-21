# Two builds in one image: the SPA (node) and the service (go). The SPA is
# built first so the Go stage never needs node, and the runtime image carries
# only the binary plus static files.
FROM kobeyoung81/base-node:26.13.1-alpine3.24-r2 AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN --mount=type=cache,id=nanosec-npm,target=/root/.npm \
    npm ci --no-audit --no-fund --prefer-offline
COPY web/ ./
RUN npm run build

FROM kobeyoung81/base-go:1.27.4-alpine3.24-r2 AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN --mount=type=cache,id=nanosec-gomod,target=/root/go/pkg/mod \
    --mount=type=cache,id=nanosec-gocache,target=/root/.cache/go-build \
    go mod download

COPY . .

ARG VERSION=dev
RUN --mount=type=cache,id=nanosec-gomod,target=/root/go/pkg/mod \
    --mount=type=cache,id=nanosec-gocache,target=/root/.cache/go-build \
    go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /bin/nanoartifacts ./cmd/nanoartifacts

FROM kobeyoung81/base-runtime:alpine3.24.4-r2

# Build metadata. All three come from the tagged commit (the version tag, the
# commit sha, and that commit's time), so two builds of one tag describe the same
# moment. `created` must be RFC 3339; the build script passes the commit time.
ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown
LABEL org.opencontainers.image.title="nanoartifacts" \
      org.opencontainers.image.version="${VERSION}" \
      org.opencontainers.image.revision="${COMMIT}" \
      org.opencontainers.image.created="${BUILD_DATE}" \
      org.opencontainers.image.source="https://gitee.com/kobeyoung81/nanoartifacts" \
      org.opencontainers.image.licenses="AGPL-3.0-only"

COPY --from=builder /bin/nanoartifacts /usr/local/bin/nanoartifacts
COPY --from=web /src/web/dist /app/web

ENV NANOARTIFACTS_WEB_DIR=/app/web
WORKDIR /app

ENTRYPOINT ["nanoartifacts"]
CMD ["serve"]
