FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src
COPY apikey_auth_plugin/go.mod apikey_auth_plugin/go.sum ./
RUN go mod download
COPY apikey_auth_plugin/ .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/apikey-auth-plugin .

FROM kong:3.9.4
COPY --from=build /out/apikey-auth-plugin /usr/local/bin/apikey-auth-plugin
COPY kong.yml /kong/kong.yml
# 2 nginx workers: the default (auto) starts one per core Docker's VM shows (20 on the Mac, 1.2 GB); 2 serve this load.
ENV KONG_DATABASE=off \
    KONG_NGINX_WORKER_PROCESSES=2 \
    KONG_DECLARATIVE_CONFIG=/kong/kong.yml \
    KONG_PLUGINS=bundled,apikey-auth-plugin \
    KONG_PLUGINSERVER_NAMES=apikey-auth-plugin \
    KONG_PLUGINSERVER_APIKEY_AUTH_PLUGIN_START_CMD=/usr/local/bin/apikey-auth-plugin \
    KONG_PLUGINSERVER_APIKEY_AUTH_PLUGIN_QUERY_CMD="/usr/local/bin/apikey-auth-plugin -dump" \
    KONG_PROXY_LISTEN="0.0.0.0:8000" \
    KONG_ADMIN_LISTEN=off \
    KONG_STATUS_LISTEN="0.0.0.0:8100" \
    KONG_PROXY_ACCESS_LOG=/dev/stdout \
    KONG_PROXY_ERROR_LOG=/dev/stderr
CMD ["kong", "docker-start"]
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD kong health
