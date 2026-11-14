FROM golang:1.27.3-alpine3.24 AS build
WORKDIR /src/catalog_service

# Dependencies first, so a change to orago's source doesn't download them again.
COPY --from=orago go.mod go.sum /src/orago/
COPY go.mod go.sum ./
RUN go mod download

COPY --from=orago . /src/orago
COPY . .
RUN CGO_ENABLED=0 go build -tags nomsgpack -trimpath -ldflags="-s -w" -o /out/catalog ./cmd

FROM alpine:3.24.3
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/catalog /app/catalog
USER app
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/app/catalog", "health"]
ENTRYPOINT ["/app/catalog"]
