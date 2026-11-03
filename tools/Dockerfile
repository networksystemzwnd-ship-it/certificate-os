# syntax=docker/dockerfile:1
# One toolbox image so nothing but Docker is needed on the host: Go, Node, Chromium (real PDF tests), psql/pg_dump.
FROM golang:1.25-bookworm AS go

FROM node:22-bookworm
COPY --from=go /usr/local/go /usr/local/go
ENV PATH="/usr/local/go/bin:/go/bin:${PATH}" GOPATH=/go GOFLAGS=-buildvcs=false
RUN apt-get update \
 && apt-get install -y --no-install-recommends chromium postgresql-client ca-certificates fonts-liberation \
 && rm -rf /var/lib/apt/lists/* \
 && printf '#!/bin/sh\nexec /usr/bin/chromium --no-sandbox "$@"\n' > /usr/local/bin/chromium-nosandbox \
 && chmod +x /usr/local/bin/chromium-nosandbox
WORKDIR /repo
