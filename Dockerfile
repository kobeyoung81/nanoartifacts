# Two builds in one image: the SPA (node) and the service (go). The SPA is
# built first so the Go stage never needs node, and the runtime image carries
# only the binary plus static files.
FROM node:24-alpine AS web

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY web/ ./
RUN npm run build

FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN go env -w GO111MODULE=on
RUN go env -w GOPROXY=https://goproxy.cn,direct
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /bin/nanoartifacts ./cmd/nanoartifacts

FROM alpine:3.19

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


RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/nanoartifacts /usr/local/bin/nanoartifacts
COPY --from=web /src/web/dist /app/web

ENV NANOARTIFACTS_WEB_DIR=/app/web
WORKDIR /app

ENTRYPOINT ["nanoartifacts"]
CMD ["serve"]
