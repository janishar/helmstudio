# Next.js standalone image: node server.js, no node_modules beyond what the build traced.
FROM node:26.13.0-alpine3.24 AS build
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund
COPY . .
ENV NEXT_TELEMETRY_DISABLED=1
RUN npm run build

# the runtime is plain alpine with node's binary and the C++ runtime it links: no npm, npx or yarn
FROM alpine:3.24.3
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
RUN apk add --no-cache libstdc++ && adduser -D -u 10001 app
COPY --from=build /usr/local/bin/node /usr/local/bin/node
COPY --from=build --chown=app /app/.next/standalone ./
COPY --from=build --chown=app /app/.next/static ./.next/static
COPY --from=build --chown=app /app/public ./public
USER app
EXPOSE 3000
CMD ["node", "server.js"]
