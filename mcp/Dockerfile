# syntax=docker/dockerfile:1.7
#
# MCP server (Streamable HTTP on :3333, POST /mcp, GET /health).
#
# The build stage runs on the builder's own architecture ($BUILDPLATFORM): the production
# dependencies are pure JavaScript (no native addons), so the node_modules produced there are
# valid for every target architecture and multi-arch builds need no QEMU emulation.

FROM --platform=$BUILDPLATFORM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN --mount=type=cache,target=/root/.npm npm ci --no-audit --no-fund
COPY tsconfig.json tsconfig.build.json ./
COPY src ./src
RUN npm run build && npm prune --omit=dev

FROM node:22-alpine@sha256:1314fb30ec37592d5845e5a160d522c0e680972aa534549940b8d73bde88481a AS runtime
ENV NODE_ENV=production PORT=3333
WORKDIR /app
COPY --from=build --chown=node:node /app/node_modules ./node_modules
COPY --from=build --chown=node:node /app/dist ./dist
# "type": "module" in package.json is what makes dist/*.js load as ES modules.
COPY --chown=node:node package.json ./
# npm, npx and corepack are only needed to build; the image runs plain `node`. Dropping them removes the
# npm-bundled packages (brace-expansion, pacote, sigstore, ...) that image scanners report.
RUN rm -rf /usr/local/lib/node_modules /usr/local/bin/npm /usr/local/bin/npx /usr/local/bin/corepack /usr/local/bin/yarn /usr/local/bin/yarnpkg /opt/yarn*
USER node
EXPOSE 3333
# busybox wget exits non-zero on connection errors and on 4xx/5xx. ${PORT} follows the runtime setting.
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 \
  CMD wget -q -O /dev/null "http://127.0.0.1:${PORT:-3333}/health" || exit 1
CMD ["node", "dist/index.js"]
