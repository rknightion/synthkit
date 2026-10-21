FROM docker:29-cli@sha256:5ebe9898293f8e1062ed0b65078c24486fb32a99dd4895d0dba0251948a7c775 AS docker-cli

FROM golang:1.27.3@sha256:f24164268fc1e741bbdc0066dbadcf1c8426c5a8489f0729c02c3905a53032fd

COPY --from=docker-cli /usr/local/bin/docker /usr/local/bin/docker
COPY --from=docker-cli /usr/local/libexec/docker/cli-plugins/docker-compose /usr/local/libexec/docker/cli-plugins/docker-compose

ENTRYPOINT ["/bin/sh"]
