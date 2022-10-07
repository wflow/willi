FROM ubuntu:22.04

ENTRYPOINT [ "/app/willi", "-c", "/app/config.conf" ]