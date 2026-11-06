# The image a Fly build machine boots (decisions.md 039): the official Go toolchain plus greyship-buildd.
# One image per supported Go minor; make build-images pushes them.
ARG GO_VERSION
FROM golang:${GO_VERSION}-alpine
COPY greyship-buildd /usr/local/bin/greyship-buildd
ENTRYPOINT ["/usr/local/bin/greyship-buildd"]
