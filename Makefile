SHELL := /bin/bash

PREFIX ?= /usr/local
SBINDIR ?= $(PREFIX)/sbin
SYSCONFDIR ?= $(PREFIX)/etc
LOCALSTATEDIR ?= $(PREFIX)/var
RUNSTATEDIR ?= $(LOCALSTATEDIR)/run
GO ?= $(shell type -a -P go | head -n 1)

VERSION ?= $(shell git describe --tags --dirty)

build:
	mkdir -p build
	cd src/enit; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/enit enit
	cd src/esvm; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/esvm esvm
	cd src/ectl; $(GO) build -ldflags "-w -X main.version=$(VERSION) -X main.sysconfdir=$(SYSCONFDIR) -X main.runstatedir=$(RUNSTATEDIR)" -o ../../build/ectl ectl

install: build/enit build/ectl
	mkdir -p $(DESTDIR)$(SBINDIR)
	mkdir -p $(DESTDIR)$(SYSCONFDIR)/esvm/services
	cp build/enit $(DESTDIR)$(SBINDIR)/enit
	cp build/esvm $(DESTDIR)$(SBINDIR)/esvm
	cp build/ectl $(DESTDIR)$(SBINDIR)/ectl

install-services:
	mkdir -p $(DESTDIR)$(SYSCONFDIR)/esvm/services
	cp services/* -t $(DESTDIR)$(SYSCONFDIR)/esvm/services

clean:
	rm -r build/

.PHONY: build
