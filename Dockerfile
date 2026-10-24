# --- control-UI build stage (Node, build-time only) ---
FROM node:24.22.1-alpine@sha256:06d7adf3e0ef61a13f1d28682726366f97a15aa1888c934d3ecec86c33918b53 AS ui
RUN apk add --no-cache ca-certificates=20261020-r0 ca-certificates-bundle=20261020-r0
WORKDIR /ui
COPY internal/control/ui/package*.json ./
RUN npm ci
COPY internal/control/ui/ ./
RUN npm run build           # emptyOutDir:false keeps .gitkeep; emits index.html + assets/

# --- Go build stage ---
FROM golang:1.27.3@sha256:f20d83a83e18dbd2a4714b8ffd4b30b287b6243962dd75608e86abcc63ba4efc AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed dist/.gitkeep placeholder with the real Vite build (COPY has no inline comments).
COPY --from=ui /ui/dist /src/internal/control/ui/dist
# VERSION is stamped as service.version onto self-obs + profiling data. REVISION is the complete
# source commit reported by `synthkit -version`; published workflows always supply both.
ARG VERSION=dev
ARG REVISION=unknown
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION} -X main.revision=${REVISION}" -o /out/synthkit ./cmd/synthkit && \
    CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/sm-provision ./cmd/sm-provision && \
    CGO_ENABLED=0 go build -o /out/synthkit-dash ./cmd/synthkit-dash

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
WORKDIR /app
COPY --from=build /out/synthkit /app/synthkit
COPY --from=build /out/sm-provision /app/sm-provision
COPY --from=build /out/synthkit-dash /app/synthkit-dash
COPY blueprints/ /app/blueprints/
# Control-plane state (Phase 6) persists under /data — mount a DIRECTORY owned by
# uid 65532 (distroless nonroot); a single-FILE mount breaks atomic save (I25).
VOLUME ["/data"]
ENTRYPOINT ["/app/synthkit"]
