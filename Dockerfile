# syntax=docker/dockerfile:1
ARG GO_VERSION=1.22
FROM golang:${GO_VERSION}-alpine AS builder
WORKDIR /src
RUN apk add --no-cache gcc musl-dev
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /bin/api ./cmd/api

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -g 1000 s3ipfs \
    && adduser -u 1000 -G s3ipfs -D -s /bin/sh s3ipfs
WORKDIR /app
COPY --from=builder /bin/api /bin/api
COPY --from=builder /src/migrations ./migrations
ENV DB_URL=postgres://s3ipfs:s3ipfs@db:5432/s3ipfs?sslmode=disable \
    STORAGE_PATH=/data \
    LISTEN_ADDR=:8080 \
    PUBLIC_HOST=http://localhost:8080
USER s3ipfs
EXPOSE 8080
ENTRYPOINT ["/bin/api"]
