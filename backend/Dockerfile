# EMBED_FRONTEND selects the build variant: "false" (default) produces a
# headless gateway, "true" produces one serving the static export at /.
# Declared before the first FROM so it can be used in a FROM instruction.
ARG EMBED_FRONTEND=false

# Build the static frontend export. Only reached when EMBED_FRONTEND=true;
# nothing depends on this stage in a headless build, so the backend image can
# be built without a working Node toolchain at all.
FROM docker.io/library/node:24.18.0-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS frontend
WORKDIR /fe
# Copy only the dependency manifests first so the install layer is cacheable
# and reproducible from the lockfile alone.
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci
COPY frontend/ ./
RUN npm run build

# Shared Go build context for both variants.
FROM docker.io/library/golang:1.26.5-alpine@sha256:0178a641fbb4858c5f1b48e34bdaabe0350a330a1b1149aabd498d0699ff5fb2 AS builder-base
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
FROM docker.io/library/alpine:3.22.2@sha256:4b7ce07002c69e8f3d704a9c5d6fd3053be500b7f1c69fc0d80990c2ad8dd412
# Fixed, unprivileged identity shared with the Compose `user:` setting.
RUN addgroup -g 10001 -S telos && adduser -S -u 10001 -G telos -H -h /app telos
WORKDIR /app
COPY --from=builder /app/telos-core .
USER 10001:10001
EXPOSE 8080
CMD ["./telos-core"]
