FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src/auth_service

# Dependencies first, so a change to helmgo's source doesn't download them again.
COPY --from=helmgo go.mod go.sum /src/helmgo/
COPY go.mod go.sum ./
RUN go mod download

COPY --from=helmgo . /src/helmgo
COPY . .
RUN CGO_ENABLED=0 go build -tags nomsgpack -trimpath -ldflags="-s -w" -o /out/auth ./cmd

FROM alpine:3.24.3
RUN adduser -D -u 10001 app && mkdir -p /app/keys && chown app /app/keys
WORKDIR /app
COPY --from=build /out/auth /app/auth
USER app
ENV PORT=8080 KEYS_DIR=/app/keys
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/app/auth", "health"]
ENTRYPOINT ["/app/auth"]
