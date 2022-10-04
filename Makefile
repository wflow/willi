GOOS := linux
GOARCH := amd64
TARGET_HOST := "changeme@localhost"

VERSION := $(shell git describe --always --dirty)

RELEASE_NAME := smtp-proxy-$(VERSION)-$(GOARCH)$(GOARM)
RELEASE_FILE := $(RELEASE_NAME).tar.gz

build:
	go build -o ../smtp-proxy

build-release:
	go build -o ../smtp-proxy -v -ldflags="-s -w -X main.version=$(VERSION)"

release: build-release
	rm -rf release/$(RELEASE_NAME)
	mkdir -p release/$(RELEASE_NAME)
	cp -r install.sh smtp-proxy smtp-proxy.service *.example release/$(RELEASE_NAME)/
	cd release && tar czf $(RELEASE_FILE) $(RELEASE_NAME)

install: release
	scp -r release/$(RELEASE_FILE) $(TARGET_HOST):
	ssh -t $(TARGET_HOST) "tar xzf $(RELEASE_FILE) && $(RELEASE_NAME)/install.sh"

clean:
	rm -f smtp-proxy
	rm -rf release
