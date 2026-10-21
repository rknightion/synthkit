FROM golang:1.27.3@sha256:6928d94d1142af58816fed5a62338d605a266ccd53a0deaab6b1096f19059261 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/receiver ./e2e/receiver/cmd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/receiver /receiver
EXPOSE 9099
ENTRYPOINT ["/receiver"]
