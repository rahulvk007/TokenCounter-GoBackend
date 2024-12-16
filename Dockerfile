# Use a multi-stage build to reduce final image size
# --- Builder Stage ---
    FROM golang:1.23.4-alpine AS builder

    WORKDIR /app
    
    # Copy go.mod and go.sum to cache dependencies
    COPY go.mod go.sum ./
    RUN go mod download
    
    # Copy source code
    COPY . .
    
    # Build the application
    RUN go build -o main .
    
    
    # --- Final Stage ---
    FROM alpine:latest
    
    WORKDIR /app
    
    # Copy the built binary from the builder stage
    COPY --from=builder /app/main /app/main
    
    # Copy .env
    COPY .env ./.env
    
    # Expose port 5001
    EXPOSE 5001
    
    # Run the application
    CMD ["/app/main"]