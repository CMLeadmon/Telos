# Static client image: the Next.js export served by unprivileged nginx.
# Build stage output is the only thing copied forward; the runtime image has no
# Node, npm, application source, dependencies, secrets or runtime configuration.
# Phase 4 replaces these tags with reviewed digests.
FROM node:24.18.0-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci
COPY . ./
RUN npm run build

FROM nginxinc/nginx-unprivileged:1.27-alpine
COPY nginx.conf /etc/nginx/nginx.conf
COPY --from=build --chown=101:101 /app/out /usr/share/nginx/html
EXPOSE 8080
USER 101:101
# Override the inherited entrypoint: its startup scripts rewrite configuration and
# cannot run against a read-only root filesystem.
ENTRYPOINT ["nginx", "-g", "daemon off;"]
CMD []
