FROM golang:1.25-alpine AS builder

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /build

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-w -s" \
    -o mock-backend \
    ./cmd/main.go

# Switch from scratch to alpine for the final stage.
# Alpine adds only 7MB but gives us wget for Docker healthchecks
# and a shell for debugging if something goes wrong during demos.
FROM alpine:3.19

RUN apk add --no-cache ca-certificates tzdata wget

WORKDIR /app
COPY --from=builder /build/mock-backend .

EXPOSE 8081

ENTRYPOINT ["/app/mock-backend"]