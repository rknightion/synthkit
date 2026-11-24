FROM golang:1.27.4@sha256:205c7636d74e0ffbe3d007162df7d74f859f5a8c8322dfb9f907977686c126cf AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/receiver ./e2e/receiver/cmd

# Debian security carries the patched trust store before the distroless rebuild.
FROM debian:bookworm-slim@sha256:e67eeead736ff7bbe258ef088bff683635c561b1fcfefad0806c1df4ab49ce0e AS trust
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
