# syntax=docker/dockerfile:1.7
# Multi-stage: build the Astro site with Node, serve dist/ with Caddy.
# Used by the Railway deployment branch (scripts/release/publish-site-branch.sh
# copies docs/site source + this Dockerfile verbatim).
FROM node:22-alpine AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
RUN npm run build
# Legacy: releases used to copy their assets into docs/site/downloads/ (deployment branch
# only). The mirror now lives in the download.ggcode.dev Worker and /downloads/latest/*
# is redirected there, so this only matters for branches that still carry the directory.
RUN if [ -d downloads ]; then mkdir -p dist/downloads && cp -R downloads/. dist/downloads/; fi

FROM caddy:2.10-alpine
WORKDIR /srv
COPY Caddyfile /etc/caddy/Caddyfile
COPY --from=build /app/dist/ .
