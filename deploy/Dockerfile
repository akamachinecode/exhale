# syntax=docker/dockerfile:1
# Built by goreleaser (dockers_v2) from the pre-compiled binary; the web UI
# is embedded in it. The build context holds one binary per platform, at
# <os>/<arch>[/<variant>]/exhale, which is exactly $TARGETPLATFORM.

# The final image has no shell, so the data directory is prepared here. A new
# named volume takes its owner from this directory; without it Docker would
# create /data owned by root and the unprivileged user could not write to it.
FROM --platform=$BUILDPLATFORM busybox:stable AS data
RUN mkdir /data && chown 65532:65532 /data

FROM gcr.io/distroless/static-debian12:nonroot
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/exhale /usr/local/bin/exhale
COPY --from=data --chown=65532:65532 /data /data
ENV EXHALE_DATA_DIR=/data \
    EXHALE_LISTEN=0.0.0.0:8484 \
    EXHALE_TRUST_LOCAL=false
VOLUME ["/data"]
EXPOSE 8484
# nonroot is uid/gid 65532.
USER nonroot:nonroot
# No shell or curl in the image, so no in-container health check. Probe
# GET /healthz from outside (docs/install/docker.md).
HEALTHCHECK NONE
ENTRYPOINT ["/usr/local/bin/exhale"]
CMD ["serve"]
