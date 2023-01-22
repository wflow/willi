FROM ubuntu:22.04

ENTRYPOINT [ "/app/lilli", "-c", "/app/lilli.conf" ]