# EMBED_FRONTEND selects the build variant: "false" (default) produces a
# headless gateway, "true" produces one serving the static export at /.
# Declared before the first FROM so it can be used in a FROM instruction.
ARG EMBED_FRONTEND=false

# Build the static frontend export. Only reached when EMBED_FRONTEND=true;
# nothing depends on this stage in a headless build, so the backend image can
# be built without a working Node toolchain at all.
FROM node:24.18.0-alpine AS frontend
WORKDIR /fe
# Copy only the dependency manifests first so the install layer is cacheable
# and reproducible from the lockfile alone.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Shared Go build context for both variants.
FROM golang:1.26.5-alpine AS builder-base
WORKDIR /app
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./

# Headless — the default target. Embeds nothing and never references the
# frontend stage.
FROM builder-base AS builder-false
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -o telos-core .

# Embedded — the operator opted into serving the web UI from the gateway.
# The //go:embed all:out directive in frontend_embed.go requires out/ to exist
# at compile time, so the copy must precede the build.
FROM builder-base AS builder-true
COPY --from=frontend /fe/out ./out
RUN CGO_ENABLED=0 GOOS=linux go build -tags embedfrontend -trimpath -o telos-core .

# Resolve the variant chosen above.
FROM builder-${EMBED_FRONTEND} AS builder

# Final Image
FROM alpine:3.22.2
WORKDIR /app
COPY --from=builder /app/telos-core .
EXPOSE 8080
CMD ["./telos-core"]
