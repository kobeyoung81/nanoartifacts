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

RUN apk add --no-cache ca-certificates tzdata

COPY --from=builder /bin/nanoartifacts /usr/local/bin/nanoartifacts
COPY --from=web /src/web/dist /app/web

ENV NANOARTIFACTS_WEB_DIR=/app/web
WORKDIR /app

ENTRYPOINT ["nanoartifacts"]
CMD ["serve"]
