# 1. Build Next.js Frontend
FROM node:20-alpine AS frontend-builder
WORKDIR /app
COPY frontend/package*.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# 2. Build Go Backend
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend-builder /app/out ./out
RUN CGO_ENABLED=0 GOOS=linux go build -o telos-core main.go

# 3. Final Image
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
