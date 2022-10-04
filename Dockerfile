FROM ubuntu:22.04

ENTRYPOINT [ "/app/smtp-proxy", "-c", "/app/config.conf" ]