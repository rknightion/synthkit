FROM docker:29-cli@sha256:eccaacfeed644c7de222ff047483568cb988dde95476fbaaf10ea2d04921bb66 AS docker-cli

FROM golang:1.27.1@sha256:3680233e3204827fbdc66088528ae6d4b3d034f51d03a99d454f6de034888244

COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose

ENTRYPOINT ["/bin/sh"]
