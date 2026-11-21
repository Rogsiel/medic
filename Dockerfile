# Built by goreleaser (dockers_v2): binaries are in the context under $TARGETPLATFORM/.
# Alpine (not scratch) so `exec` checks with `shell:` and HTTPS (CA certs) work.
FROM alpine:3.22
RUN apk add --no-cache ca-certificates
ARG TARGETPLATFORM
COPY $TARGETPLATFORM/medic /usr/local/bin/medic
WORKDIR /work
ENTRYPOINT ["medic"]
