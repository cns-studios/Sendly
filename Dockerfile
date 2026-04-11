FROM golang:1.22-alpine AS builder

WORKDIR /app

RUN apk add --no-cache gcc musl-dev

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o server ./cmd/server

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o admin ./cmd/admin

FROM alpine:3.19

WORKDIR /app

RUN apk add --no-cache ca-certificates tzdata

RUN adduser -D -g '' appuser

RUN mkdir -p /data && chown appuser:appuser /data

COPY --from=builder /app/server .
COPY --from=builder /app/admin .

COPY --from=builder /app/web ./web
COPY --from=builder /app/db ./db

COPY --from=builder /app/wordlist.txt ./web/static/wordlist.txt

RUN chown -R appuser:appuser /app

RUN mkdir -p /chunks && chown appuser:appuser /chunks

USER appuser

EXPOSE 8085

HEALTHCHECK --interval=30s --timeout=3s --start-period=5s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8085/health || exit 1

CMD ["./server"]