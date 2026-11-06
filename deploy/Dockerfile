# syntax=docker/dockerfile:1
# Built by goreleaser from the pre-compiled binary (the web UI is embedded).
FROM gcr.io/distroless/static-debian12:nonroot
COPY exhale /usr/local/bin/exhale
ENV EXHALE_DATA_DIR=/data \
    EXHALE_LISTEN=0.0.0.0:8484 \
    EXHALE_TRUST_LOCAL=false
VOLUME ["/data"]
EXPOSE 8484
USER nonroot:nonroot
HEALTHCHECK NONE
ENTRYPOINT ["/usr/local/bin/exhale"]
CMD ["serve"]
