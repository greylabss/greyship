# The module proxy build sandboxes talk to. The binary is cross-compiled by deploy/sandbox-init.sh.
FROM gcr.io/distroless/static-debian12:nonroot
COPY greyship-modproxy /greyship-modproxy
ENV GREYSHIP_MODPROXY_CACHE=/cache
VOLUME /cache
EXPOSE 8080
ENTRYPOINT ["/greyship-modproxy"]
