# syntax=docker/dockerfile:1.7
#
# Next.js frontend, standalone output (node server.js on :3000).
#
# IMPORTANT: the URLs marked "build time" are baked into the image. Changing one means rebuilding
# the image (the release workflow passes them from the repo variables API_PUBLIC_URL,
# MCP_PUBLIC_URL and optionally API_INTERNAL_URL; see deploy/README.md):
#   * API_INTERNAL_URL is baked into the /api/v1/* rewrite (routes-manifest). It must be the
#     address of the backend *inside the compose network* (service "backend" => http://backend:8080).
#   * NEXT_PUBLIC_* are inlined into the browser bundle.
#
# Multi-arch: the deps and build stages run on the builder's own architecture ($BUILDPLATFORM),
# only the final stage is per target, so no QEMU emulation is needed. The standalone trace also
# contains sharp's native binaries for the *build* architecture; they are only used by next/image
# optimization, which this app does not use.

FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS deps
WORKDIR /app
COPY package.json package-lock.json* ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund

FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS build
WORKDIR /app
COPY --from=deps /app/node_modules ./node_modules
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
# Build time: see the note at the top of this file.
ARG API_INTERNAL_URL=http://backend:8080
ARG NEXT_PUBLIC_MOCK_API=false
ARG NEXT_PUBLIC_MCP_URL=http://localhost:3333/mcp
ARG NEXT_PUBLIC_API_URL=http://localhost:8080
ENV API_INTERNAL_URL=$API_INTERNAL_URL NEXT_PUBLIC_MOCK_API=$NEXT_PUBLIC_MOCK_API \
    NEXT_PUBLIC_MCP_URL=$NEXT_PUBLIC_MCP_URL NEXT_PUBLIC_API_URL=$NEXT_PUBLIC_API_URL
RUN npm run build

FROM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS runner
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
# Non-root: the "node" user (uid 1000) ships with the base image, so no RUN step is needed here.
COPY --from=build --chown=node:node /app/.next/standalone ./
COPY --from=build --chown=node:node /app/.next/static ./.next/static
COPY --from=build --chown=node:node /app/public ./public
# npm, npx and corepack are only needed to build; the image runs plain `node`. Dropping them removes the
# npm-bundled packages (brace-expansion, pacote, sigstore, ...) that image scanners report.
RUN rm -rf /usr/local/lib/node_modules /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg /opt/yarn*
USER node
EXPOSE 3000
# /login is a public page that renders without the backend, so this checks the Next server only.
HEALTHCHECK --interval=10s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-3000}/login" || exit 1
CMD ["node", "server.js"]
