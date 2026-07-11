FROM node:22.16.0-alpine AS dependencies
WORKDIR /app
RUN corepack enable
COPY services/dropmong-web/package.json services/dropmong-web/pnpm-lock.yaml services/dropmong-web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

FROM node:22.16.0-alpine AS build
WORKDIR /app
RUN corepack enable
COPY --from=dependencies /app/node_modules ./node_modules
COPY services/dropmong-web ./
RUN pnpm lint && pnpm typecheck && pnpm test && pnpm build

FROM node:22.16.0-alpine AS runtime
WORKDIR /app
ENV NODE_ENV=production
ENV PORT=3000
ENV HOSTNAME=0.0.0.0
RUN addgroup -S nodejs && adduser -S nextjs -G nodejs
COPY --from=build --chown=nextjs:nodejs /app/.next/standalone ./
COPY --from=build --chown=nextjs:nodejs /app/.next/static ./.next/static
COPY --from=build --chown=nextjs:nodejs /app/public ./public
USER nextjs
EXPOSE 3000
HEALTHCHECK --interval=10s --timeout=3s --start-period=20s --retries=3 CMD ["node", "-e", "require('http').get('http://127.0.0.1:3000/readyz', (response) => process.exit(response.statusCode === 200 ? 0 : 1)).on('error', () => process.exit(1))"]
CMD ["node", "server.js"]
