# --- control-UI build stage (Node, build-time only) ---
FROM node:24-alpine@sha256:44fce696f38ba8c987c4db89954051000bbd05ffd8d8659ba15ed4a8602e937f AS ui
WORKDIR /ui
COPY internal/control/ui/package*.json ./
RUN npm ci
COPY internal/control/ui/ ./
RUN npm run build           # emptyOutDir:false keeps .gitkeep; emits index.html + assets/

# --- Go build stage ---
FROM golang:1.27.2@sha256:820153096fda4de6ac1b7194d12e416b7dd8f4e9a33d17e0345b111b945162d8 AS build
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
