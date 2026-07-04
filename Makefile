VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X github.com/shiranzby/cftunnelX/cmd.Version=$(VERSION)

build:
	go build -buildvcs=false -ldflags "$(LDFLAGS)" -o cftunnelX .

build-windows:
	go build -buildvcs=false -ldflags "$(LDFLAGS)" -o cftunnelX.exe .

clean:
	rm -f cftunnelX cftunnelX.exe

web:
	go build -buildvcs=false -ldflags "$(LDFLAGS)" -o cftunnelX.exe . && ./cftunnelX.exe web

.PHONY: build build-windows clean web
