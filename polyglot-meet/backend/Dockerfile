FROM golang:1.23-alpine AS builder
RUN apk add --no-cache git ca-certificates

WORKDIR /app

# Copy go.mod first, then all source
COPY go.mod ./
COPY . .

# Generate go.sum and download deps in one step
RUN go mod tidy && \
    CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o polyglot-meet .

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/polyglot-meet .
EXPOSE 8080
CMD ["./polyglot-meet"]
