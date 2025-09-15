# Multi-stage build for the Go QUIC application
FROM golang:1.23-alpine AS builder

# Install git and ca-certificates
RUN apk add --no-cache git ca-certificates

# Set working directory
WORKDIR /app

# Copy go mod files
COPY go.mod go.sum ./

# Download dependencies
RUN go mod download

# Copy source code
COPY main.go metrics.go quic_optimizations.go ./

# Build the application
RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main *.go

# Final stage
FROM alpine:latest

# Install ca-certificates for HTTPS
RUN apk --no-cache add ca-certificates

WORKDIR /root/

# Copy the binary from builder
COPY --from=builder /app/main .

# Copy certificates (if they exist)
COPY localhost+2.pem localhost+2-key.pem ./

# Copy static files
COPY static/ ./static/

# Expose the port
EXPOSE 9443
EXPOSE 8080

# Run the binary
CMD ["./main"]
