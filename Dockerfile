# Build whytop from source. For released images see Dockerfile.release, which
# packages the binary goreleaser has already built.
FROM golang:1.26-bookworm AS build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o /whytop ./cmd/whytop

# systemd is here for its client tools, not to run it: systemctl and
# journalctl are what the Units tab and the journal shell out to. They talk to
# whatever systemd owns the sockets mounted into the container — the host's,
# if you mount the host's — so without those mounts whytop still runs, and
# says plainly that systemd isn't reachable rather than appearing to hang.
#
# procps and gdb are deliberately absent: nothing shells out to ps, and the
# "close a descriptor" action needs gdb, which is a debugger in a monitoring
# image. whytop reports that it is missing if you ask for that action.
FROM debian:stable-slim
RUN apt-get update \
 && apt-get install -y --no-install-recommends systemd \
 && rm -rf /var/lib/apt/lists/*

COPY --from=build /whytop /usr/bin/whytop

# No USER line on purpose. whytop needs root (or the capabilities in the
# README) to read other users' sockets, per-process I/O and open files, and
# dropping to a non-root user here would only move the problem to a flag
# every single run has to pass back.
ENTRYPOINT ["/usr/bin/whytop"]
