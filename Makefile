.PHONY: build linux windows clean

build:
	go build -ldflags="-s -w" -o sshmenu .

linux:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -o sshmenu .

windows:
	GOOS=windows GOARCH=amd64 go build -ldflags="-s -w" -o sshmenu.exe .

all: linux windows

clean:
	rm -f sshmenu sshmenu.exe
