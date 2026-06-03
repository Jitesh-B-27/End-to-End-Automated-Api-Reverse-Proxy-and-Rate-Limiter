# Build stage
FROM golang:1.22-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o mock-backend ./cmd/main.go

# Run stage
FROM alpine:latest

WORKDIR /app

COPY --from=builder /app/mock-backend .
COPY --from=builder /app/.env .

EXPOSE 8081

CMD ["./mock-backend"]
