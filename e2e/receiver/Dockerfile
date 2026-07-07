FROM golang:1.26@sha256:b900de91b15b2e2953d930ece1d0ecff0a1590ab2006088d20dcf0f56f1e979f AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/receiver ./e2e/receiver/cmd

FROM gcr.io/distroless/static-debian12:nonroot@sha256:d093aa3e30dbadd3efe1310db061a14da60299baff8450a17fe0ccc514a16639
COPY --from=build /out/receiver /receiver
EXPOSE 9099
ENTRYPOINT ["/receiver"]
