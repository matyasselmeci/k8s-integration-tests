# Test-runner image for the Pelican client. The Pelican client image is based on
# distroless, so it contains no shell or standard command-line tools. Add
# busybox so tests have a shell and the usual utilities.
#
# Build with, e.g.:
#   docker build --build-arg VERSION=v26.0.2 -t pelicanclient:v26.0.2 images/pelican-client

# Version tag of the Pelican client image to build from, e.g. "v26.0.2".
ARG VERSION

# Pull busybox for the architecture of the image being built, not the build
# host, so the binary we copy is executable in the final image when
# cross-building. BuildKit provides TARGETPLATFORM automatically. The musl
# variant is statically linked, so it runs in the distroless client image
# without any libc.
FROM --platform=${TARGETPLATFORM} docker.io/library/busybox:musl AS busybox

FROM hub.osg-htc.org/pelican-ci/client:${VERSION}

# /bin in the client image is a symlink to /usr/bin.
COPY --from=busybox /bin/busybox /usr/bin/busybox

# The client image runs as nonroot and has no /bin/sh, hence the temporary user
# switch and the exec-form RUN (which bypasses the shell). Restore the base
# image's user afterwards.
USER root
RUN ["/usr/bin/busybox", "--install", "-s", "/bin"]
USER nonroot
