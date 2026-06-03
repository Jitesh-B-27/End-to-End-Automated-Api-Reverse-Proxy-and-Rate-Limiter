# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod ./
# RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o api-gateway ./cmd/main.go

# Run stage
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/api-gateway .
COPY --from=builder /app/.env .

EXPOSE 8080

CMD ["./api-gateway"]
