# Installation paths
PREFIX ?= /usr/local
SBINDIR ?= $(PREFIX)/sbin
SYSCONFDIR ?= $(PREFIX)/etc
LOCALSTATEDIR ?= $(PREFIX)/var
RUNSTATEDIR ?= $(LOCALSTATEDIR)/run

# Compilers and tools
GO ?= $(shell type -a -P go | head -n 1)

# Build-time variables
VERSION ?= $(shell git describe --tags --dirty)

build:
	mkdir -p build
	cd src/enit; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/enit enit
	cd src/esvm; $(GO) build -ldflags "-w -X main.version=$(VERSION)" -o ../../build/esvm esvm
	cd src/ectl; $(GO) build -ldflags "-w -X main.version=$(VERSION) -X main.sysconfdir=$(SYSCONFDIR) -X main.runstatedir=$(RUNSTATEDIR)" -o ../../build/ectl ectl

install: build/enit build/ectl build/esvm
	# Create directories
	install -d $(DESTDIR)$(SBINDIR)
	# Install binaries
	install -m755 build/{enit,esvm,ectl} -t $(DESTDIR)$(SBINDIR)

install-services:
	# Create directory
	install -d $(DESTDIR)$(SYSCONFDIR)/esvm/services
	# Install services
	install -m644 services/*.esv -t $(DESTDIR)$(SYSCONFDIR)/esvm/services

uninstall:
	-rm -f $(DESTDIR)$(SBINDIR)/{enit,esvm,ectl}
	-rm -rf $(DESTDIR)$(SYSCONFDIR)/esvm

clean:
	rm -r build/

.PHONY: build install install-services uninstall clean
