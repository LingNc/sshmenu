VERSION := $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)

.PHONY: build linux windows clean

build:
	go build -ldflags="-s -w -X main.version=$(VERSION)" -o sshmenu .

linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w -X main.version=$(VERSION)" -o sshmenu .

windows:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w -X main.version=$(VERSION)" -o sshmenu.exe .

all: linux windows

clean:
	rm -f sshmenu sshmenu.exe
