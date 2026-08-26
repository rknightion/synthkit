# --- control-UI build stage (Node, build-time only) ---
FROM node:24-alpine@sha256:d32cdf619f63fe0471182d08996dd516c6275bb5fd31ae06e55a570bd9e1ad43 AS ui
WORKDIR /ui
COPY internal/control/ui/package*.json ./
RUN npm ci
COPY internal/control/ui/ ./
RUN npm run build           # emptyOutDir:false keeps .gitkeep; emits index.html + assets/

# --- Go build stage ---
FROM golang:1.27.0@sha256:0ecdc2a9f6156af6451080bfe3d8382a662fcc4e209608c6f919e643453514c1 AS build
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
    CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/sm-provision ./cmd/sm-provision

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
WORKDIR /app
COPY --from=build /out/synthkit /app/synthkit
COPY --from=build /out/sm-provision /app/sm-provision
COPY blueprints/ /app/blueprints/
# Control-plane state (Phase 6) persists under /data — mount a DIRECTORY owned by
# uid 65532 (distroless nonroot); a single-FILE mount breaks atomic save (I25).
VOLUME ["/data"]
ENTRYPOINT ["/app/synthkit"]
