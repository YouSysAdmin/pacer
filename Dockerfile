# syntax=docker/dockerfile:1.7

# Minimal Dockerfile that wraps a pre-built pacer binary in a
# distroless runtime image. Both flows feed it the same way:
#
#   - `make build-docker` -- the Makefile compiles frontend + Go
#     binary on the host, stages it under bin/docker/, and runs
#     `docker build -f Dockerfile bin/docker/`.
#   - goreleaser's `dockers:` section -- goreleaser cross-compiles
#     the binary once (matching the archive bits exactly) and stages
#     it alongside this Dockerfile.
#
# Either way, the build context contains exactly one file: a Linux
# `pacer` binary at the context root.

# Tiny stage that exists only so we can mkdir + chown -- distroless
# has no shell, so /data must arrive pre-owned.
FROM busybox:stable AS prep
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --chown=65532:65532 pacer /app/pacer
COPY --from=prep --chown=65532:65532 /out/data /data
VOLUME ["/data"]
ENV TZ=UTC
EXPOSE 3000
USER nonroot:nonroot
ENTRYPOINT ["/app/pacer"]
CMD ["serve", "--config", "/etc/pacer/pacer.yaml"]
