FROM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/receiver ./e2e/receiver/cmd

# Debian security carries the patched trust store before the distroless rebuild.
FROM debian:bookworm-slim@sha256:7ff6ba31527b9e336162fa061746c366c4b2032f4d2cff2078bea43f78efe3f8 AS trust
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates=20250419~deb12u2 && \
    dpkg-query -s ca-certificates > /ca-certificates.status && \
    mkdir /selected-certs && cp -L /etc/ssl/certs/*.pem /selected-certs/

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
# A fresh directory prevents COPY overlay from retaining distrusted base roots.
COPY --from=trust /selected-certs/ /etc/ssl/certs-patched/
COPY --from=trust /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs-patched/ca-certificates.crt
ENV SSL_CERT_FILE=/etc/ssl/certs-patched/ca-certificates.crt SSL_CERT_DIR=/etc/ssl/certs-patched
COPY --from=trust /ca-certificates.status /var/lib/dpkg/status.d/ca-certificates
COPY --from=build /out/receiver /receiver
EXPOSE 9099
ENTRYPOINT ["/receiver"]
