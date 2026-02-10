# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /src

# Cache dependencies
COPY go.mod go.sum ./
RUN go mod download

# Build the binary
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
    -ldflags="-s -w -X main.version=dev -X main.commit=$(echo unknown)" \
    -o /wormsign-controller \
    ./cmd/controller/

# Runtime stage
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=builder /wormsign-controller /wormsign-controller

USER 65534:65534

ENTRYPOINT ["/wormsign-controller"]
