FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY cmd ./cmd
COPY internal ./internal

RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o server ./cmd/server && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o admin ./cmd/admin

FROM alpine:3.19

WORKDIR /app

ENV PORT=8085

RUN apk add --no-cache ca-certificates tzdata

# A fixed numeric UID, so Kubernetes can verify runAsNonRoot. 1000 is what
# the image used before, which keeps existing volumes writable.
RUN adduser -D -u 1000 -g '' appuser

COPY --from=builder /app/server /app/admin ./
COPY web ./web
COPY db ./db
COPY wordlist.txt ./web/static/wordlist.txt

# Everything under /app is read-only at runtime (Kubernetes mounts the root
# filesystem read-only); only the storage directories are written.
RUN mkdir -p /data /chunks && chown appuser:appuser /data /chunks

USER 1000:1000

EXPOSE 8085

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:${PORT:-8085}/health || exit 1

CMD ["./server"]
