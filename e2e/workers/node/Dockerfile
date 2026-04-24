FROM node:20-alpine AS build
RUN apk add --no-cache protobuf-dev
WORKDIR /app

# Copy SDK TypeScript source for direct compilation
COPY workflow-sdk-node/src ./sdk-src
COPY proto ./proto

# Install worker dependencies (undici + dev tools)
COPY e2e/workers/node/package.json ./
RUN npm install

# Generate protos
RUN mkdir -p sdk-src/generated && \
    protoc \
    --plugin=protoc-gen-ts_proto=./node_modules/.bin/protoc-gen-ts_proto \
    --ts_proto_out=sdk-src/generated \
    --ts_proto_opt=env=node,outputServices=grpc-js,esModuleInterop=true \
    -I ./proto \
    ./proto/workflow/v1/engine.proto

# Copy tsconfig and worker source, then compile everything together
COPY e2e/workers/node/tsconfig.json .
COPY e2e/workers/node/src ./src
RUN npx tsc

FROM node:20-alpine
WORKDIR /app
COPY --from=build /app/dist ./dist
COPY --from=build /app/node_modules ./node_modules
CMD ["node", "dist/src/worker.js"]
