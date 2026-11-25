# The release stack's first-start image (deploy/release/compose.yml, services `secrets`): .tools/make-secrets and the
# files the base stack bind-mounts from the repo (nats.conf, postgres/init.sh, the one-shots' scripts, the built-in
# plugins), so compose.yml needs no source. Build from the repo root:
#   docker build -f deploy/release/setup.Dockerfile .
FROM alpine:3.24.3
RUN apk add --no-cache openssl
COPY .tools/make-secrets .tools/nats-setup .tools/storage-setup /stack/.tools/
COPY deploy/nats /stack/deploy/nats
COPY deploy/postgres/init.sh /stack/deploy/postgres/
COPY plugins /stack/plugins
COPY deploy/release/setup.sh /stack/setup.sh
ENTRYPOINT ["sh", "/stack/setup.sh"]
