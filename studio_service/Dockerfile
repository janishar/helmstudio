FROM golang:1.27.4-alpine3.24 AS build
WORKDIR /src/studio_service

# Dependencies first, so a change to helmgo's source doesn't download them again.
COPY --from=helmgo go.mod go.sum /src/helmgo/
COPY go.mod go.sum ./
RUN go mod download

COPY --from=helmgo . /src/helmgo
COPY . .
RUN CGO_ENABLED=0 go build -tags nomsgpack -trimpath -ldflags="-s -w" -o /out/studio ./cmd

FROM alpine:3.24.3
RUN adduser -D -u 10001 app
WORKDIR /app
COPY --from=build /out/studio /app/studio
USER app
ENV PORT=8080
EXPOSE 8080
HEALTHCHECK --interval=5s --timeout=3s --retries=10 CMD ["/app/studio", "health"]
ENTRYPOINT ["/app/studio"]
