FROM golang:1.26@sha256:2005724102f45917a63e9d092fc0e4ea56ea575048ce147caad5f5f61502c365 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/receiver ./e2e/receiver/cmd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:f5b485ea962d9bd1186b2f6b3a061191539b905b82ec395de78cbfae51f20e35
COPY --from=build /out/receiver /receiver
EXPOSE 9099
ENTRYPOINT ["/receiver"]
