# Static client image: the Next.js export served by unprivileged nginx.
# Build stage output is the only thing copied forward; the runtime image has no
# Node, npm, application source, dependencies, secrets or runtime configuration.
# Phase 4 replaces these tags with reviewed digests.
FROM docker.io/library/node:24.18.0-alpine@sha256:a0b9bf06e4e6193cf7a0f58816cc935ff8c2a908f81e6f1a95432d679c54fbfd AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
RUN npm run build

FROM docker.io/nginxinc/nginx-unprivileged:1.31.7-alpine@sha256:378d0c32f6562f7fd9629170797f57d2b953aa09be983b3daaf4809bb039ca00
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build --chown=101:101 /app/out /usr/share/nginx/html
EXPOSE 8080
USER 101:101
# Override the inherited entrypoint: its startup scripts rewrite configuration and
# cannot run against a read-only root filesystem.
ENTRYPOINT ["nginx", "-g", "daemon off;"]
CMD []
