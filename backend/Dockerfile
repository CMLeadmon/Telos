# Build the static frontend export
FROM node:22-alpine AS frontend
WORKDIR /fe
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Build Go Backend (embeds the frontend export via //go:embed all:out)
FROM golang:1.22-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /fe/out ./out
RUN CGO_ENABLED=0 GOOS=linux go build -o telos-core main.go

# Final Image
FROM alpine:latest
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
