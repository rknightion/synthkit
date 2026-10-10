# --- control-UI build stage (Node, build-time only) ---
FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS ui
WORKDIR /ui
COPY internal/control/ui/package*.json ./
RUN npm ci
COPY internal/control/ui/ ./
RUN npm run build           # emptyOutDir:false keeps .gitkeep; emits index.html + assets/

# --- Go build stage ---
FROM golang:1.27.2@sha256:5bc7f572bbaa98885a3a1fd9c0aa76b59e3e14e8628bfc316bbfd0c701e4818c AS build
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

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
WORKDIR /app
COPY --from=build /out/synthkit /app/synthkit
COPY --from=build /out/sm-provision /app/sm-provision
COPY --from=build /out/synthkit-dash /app/synthkit-dash
COPY blueprints/ /app/blueprints/
# Control-plane state (Phase 6) persists under /data — mount a DIRECTORY owned by
# uid 65532 (distroless nonroot); a single-FILE mount breaks atomic save (I25).
VOLUME ["/data"]
ENTRYPOINT ["/app/synthkit"]
