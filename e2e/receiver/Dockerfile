FROM golang:1.27.4@sha256:5f1b98fa02fe396ebce2a0227a0e08309e11792915b9dd2e1f71053d7e7e6666 AS build
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
