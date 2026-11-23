# SeaweedFS with only the `weed` binary (static) on Alpine: upstream also ships weed-volume, weed-worker and
# weed-vector, which `weed server` never runs (1.03 GB image). Kept from upstream: filer.toml (filer metadata in
# /data/filerldb2), the seaweed user (uid/gid 1000, the owner of existing seaweeddata volumes), FIPS mode on (as its
# entrypoint sets) and /data as the work dir. Its entrypoint's server flags are in deploy/docker-compose.yml.
FROM chrislusf/seaweedfs:4.53 AS upstream

FROM alpine:3.24.3
COPY --from=upstream /usr/bin/weed /usr/bin/weed
COPY --from=upstream /etc/seaweedfs/filer.toml /etc/seaweedfs/filer.toml
RUN addgroup -g 1000 seaweed && adduser -D -u 1000 -G seaweed seaweed && mkdir /data && chown seaweed:seaweed /data
ENV GODEBUG=fips140=on
USER seaweed
WORKDIR /data
VOLUME /data
ENTRYPOINT ["weed"]
