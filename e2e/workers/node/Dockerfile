FROM node:24-alpine AS build
WORKDIR /app

# Install the exact candidate verified by the external consumer test.
COPY workflow-sdk-node/artifacts/sdk.tgz workflow-sdk-node/artifacts/SHA256SUMS ./
RUN sha256sum -c SHA256SUMS
COPY e2e/workers/node/package.json e2e/workers/node/package-lock.json ./
RUN npm ci && npm install --package-lock=false ./sdk.tgz

COPY e2e/workers/node/tsconfig.json .
COPY e2e/workers/node/src ./src
RUN npx tsc && npm prune --omit=dev --package-lock=false

FROM node:24-alpine
WORKDIR /app
COPY --from=build /app/package.json ./
COPY --from=build /app/dist ./dist
COPY --from=build /app/node_modules ./node_modules
CMD ["node", "dist/worker.js"]
