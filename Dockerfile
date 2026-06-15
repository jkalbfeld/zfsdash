# Build stage
FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o zfsdash .

# Runtime stage
FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /build/zfsdash /app/zfsdash
EXPOSE 8080
ENTRYPOINT ["/app/zfsdash"]
CMD ["-config", "/etc/zfsdash/config.yaml"]
