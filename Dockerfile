FROM golang:1.25-alpine AS builder

WORKDIR /src
RUN apk add --no-cache git ca-certificates
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -buildvcs=false -ldflags="-s -w" -o /out/cftunnelX .

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata
WORKDIR /app
COPY --from=builder /out/cftunnelX /app/cftunnelX
RUN adduser -D -H -u 10001 cftunnelx && mkdir -p /app/config /app/log && chown -R cftunnelx:cftunnelx /app
USER cftunnelx
EXPOSE 7860
VOLUME ["/app/config", "/app/log"]
ENTRYPOINT ["/app/cftunnelX"]
CMD ["web", "--open=false"]
