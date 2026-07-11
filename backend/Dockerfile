# Build Go Backend
# The `out/` directory contains the static frontend assets to embed.
# During the design overhaul, it holds a minimal placeholder page.
# When the new frontend is ready, restore the multi-stage build that
# compiles it from `frontend/` before this stage.
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 GOOS=linux go build -o telos-core main.go

# Final Image
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
