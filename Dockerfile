# syntax=docker/dockerfile:1
FROM golang:1.23-alpine AS builder

WORKDIR /src

# Install git (needed for go mod download with private/fresh modules)
RUN apk add --no-cache git

# Copy dependency manifests first (layer cache)
COPY go.mod ./

# Regenerate go.sum with GONOSUMDB so stale checksums don't block the build
ENV GONOSUMDB=*
ENV GOFLAGS=-mod=mod
RUN go mod download

# Copy all source
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags "-s -w" -o /zfsdash .

# ---
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app
COPY --from=builder /zfsdash /usr/local/bin/zfsdash

# Config and SSH key mount points
VOLUME ["/app/config.yaml", "/etc/zfsdash"]

EXPOSE 8080

ENTRYPOINT ["/usr/local/bin/zfsdash"]
CMD ["-config", "/app/config.yaml"]
