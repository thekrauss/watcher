FROM golang:1.23-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o watcher ./cmd/main.go

FROM alpine:3.18
WORKDIR /root/

RUN apk --no-cache add ca-certificates
COPY --from=builder /app/watcher .
CMD ["./watcher"]