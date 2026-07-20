# Build the static frontend export
FROM node:24.18.0-alpine AS frontend
WORKDIR /fe
# Copy only the dependency manifests first so the install layer is cacheable
# and reproducible from the lockfile alone.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Build Go Backend (embeds the frontend export via //go:embed all:out)
FROM golang:1.26.5-alpine AS builder
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
COPY --from=frontend /fe/out ./out
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o telos-core .

# Final Image
FROM alpine:3.22.2
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
