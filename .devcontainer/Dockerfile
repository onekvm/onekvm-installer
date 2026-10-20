FROM golang:1.26-bookworm

RUN groupadd --gid 1000 vscode \
    && useradd --uid 1000 --gid vscode --create-home --shell /bin/bash vscode \
    && mkdir -p /go/pkg \
    && chown -R vscode:vscode /go

USER vscode
