# --- control-UI build stage (Node, build-time only) ---
FROM node:24-alpine AS ui
WORKDIR /ui
COPY internal/control/ui/package*.json ./
RUN npm ci
COPY internal/control/ui/ ./
RUN npm run build           # emptyOutDir:false keeps .gitkeep; emits index.html + assets/

# --- Go build stage ---
FROM golang:1.26 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Replace the committed dist/.gitkeep placeholder with the real Vite build (COPY has no inline comments).
COPY --from=ui /ui/dist /src/internal/control/ui/dist
# VERSION is stamped as service.version onto self-obs + profiling data (defaults to "dev").
ARG VERSION=dev
RUN CGO_ENABLED=0 go build -ldflags "-X main.version=${VERSION}" -o /out/synthkit ./cmd/synthkit

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/synthkit /app/synthkit
COPY blueprints/ /app/blueprints/
# Control-plane state (Phase 6) persists under /data — mount a DIRECTORY owned by
# uid 65532 (distroless nonroot); a single-FILE mount breaks atomic save (I25).
VOLUME ["/data"]
ENTRYPOINT ["/app/synthkit"]
