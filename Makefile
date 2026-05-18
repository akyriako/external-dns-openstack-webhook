.PHONY: all build

PROJ_NAME = external-dns-opentelekomcloud-webhook

all: build

build:
	go build -o build/bin/$(PROJ_NAME) ./cmd/webhook
