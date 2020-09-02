#!/bin/sh
export CGO_ENABLED=1
rm -rf release
mkdir release
GOARCH=mipsle CC=mipsel-linux-gnu-gcc go build -ldflags='-s -w -extldflags -static' -o memetagfs.mipsel
7z a release/memetagfs.mipsel.zip memetagfs.mipsel
GOARCH=arm CC=arm-linux-gnueabi-gcc go build -ldflags='-s -w -extldflags -static' -o memetagfs.arm
7z a release/memetagfs.arm.zip memetagfs.arm
GOARCH=386 go build -ldflags='-s -w -extldflags -static' -o memetagfs.386
7z a release/memetagfs.386.zip memetagfs.386
GOARCH=amd64 go build -ldflags='-s -w -extldflags -static' -o memetagfs.amd64
7z a release/memetagfs.amd64.zip memetagfs.amd64
