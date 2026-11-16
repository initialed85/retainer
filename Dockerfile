# The binary is cross-compiled by CI rather than built in a golang stage,
# matching the house pattern (rooter/camry): the build context then carries
# only the artifact that was tested, and the image needs no toolchain.
# .dockerignore whitelists tmp/retainer out of an otherwise excluded tmp/.
FROM ubuntu:22.04

ENV TZ Australia/Perth
ENV DEBIAN_FRONTEND noninteractive

RUN apt-get update && apt-get install -y --no-install-recommends tzdata curl ca-certificates \
    && rm -rf /var/lib/apt/lists/*

RUN dpkg-reconfigure -f noninteractive tzdata

WORKDIR /srv/

COPY ./tmp/retainer /srv/bin/retainer

EXPOSE 8088

# Default bind is all interfaces on 8088; the k8s deployment's probes hit
# /healthz, which needs no key.
HEALTHCHECK --interval=10s --timeout=5s --start-period=5s --retries=3 \
    CMD ["curl", "-fsS", "http://127.0.0.1:8088/healthz"]

ENTRYPOINT ["/srv/bin/retainer"]