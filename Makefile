SHELL := /bin/bash

PREFIX ?= /usr/local
SBINDIR ?= $(PREFIX)/sbin
SYSCONFDIR ?= $(PREFIX)/etc
LOCALSTATEDIR ?= $(PREFIX)/var
RUNSTATEDIR ?= $(LOCALSTATEDIR)/run
GO ?= $(shell type -a -P go | head -n 1)

# Set version variable
ifeq ($(VERSION),)
	COMMIT := $(shell git rev-parse --short HEAD)
	TAG_COMMIT := $(shell git rev-list --abbrev-commit --tags --max-count=1)
	TAG := $(shell git describe --abbrev=0 --tags ${TAG_COMMIT} 2>/dev/null || true)
	VERSION := $(COMMIT)
	ifeq ($(COMMIT), $(TAG_COMMIT))
		VERSION := $(TAG)
	endif
	ifneq ($(shell git status --porcelain),)
		VERSION := $(VERSION)-dirty
	endif
endif

build:
	mkdir -p build
	cd cmd/enit; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/enit enit
	cd cmd/esvm; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/esvm esvm
	cd cmd/ectl; $(GO) build -ldflags "-w -X main.version=$(VERSION) -X main.sysconfdir=$(SYSCONFDIR) -X main.runstatedir=$(RUNSTATEDIR)" -o ../../build/ectl ectl

install: build/enit build/ectl
	mkdir -p $(DESTDIR)$(SBINDIR)
	mkdir -p $(DESTDIR)$(SYSCONFDIR)/esvm/services
	cp build/enit $(DESTDIR)$(SBINDIR)/enit
	cp build/enit $(DESTDIR)$(SBINDIR)/esvm
	cp build/ectl $(DESTDIR)$(SBINDIR)/ectl

install-services:
	mkdir -p $(DESTDIR)$(SYSCONFDIR)/esvm/services
	cp services/* -t $(DESTDIR)$(SYSCONFDIR)/esvm/services

clean:
	rm -r build/

.PHONY: build
