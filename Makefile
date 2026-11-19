# DevicePort v0.1.5 build helpers
GO ?= go
# Go 1.23 is used for the Windows binary because Go 1.26's UDP socket path
# fails under Wine (WSAEOPNOTSUPP). Override GO_WIN if it is not on PATH.
GO_WIN ?= $(GO)
VERSION := v0.1.5
BIN := bin
DIST := dist

.PHONY: all build release build-windows build-android test fmt vet clean desktop android-apk

all: fmt vet test build

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

# A single executable contains both `server` and `node` subcommands.
build:
	mkdir -p $(BIN) $(DIST)
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/deviceport ./cmd/deviceport
	cp $(BIN)/deviceport $(DIST)/deviceport-linux-amd64
	CGO_ENABLED=0 $(GO) build -trimpath -o $(BIN)/deviceportctl ./cmd/deviceportctl

build-windows:
	mkdir -p $(BIN) $(DIST)
	GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO_WIN) build -modfile=windows.mod -trimpath -o $(BIN)/deviceport.exe ./cmd/deviceport
	GOTOOLCHAIN=local GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO_WIN) build -modfile=windows.mod -trimpath -o $(BIN)/deviceportctl.exe ./cmd/deviceportctl
	cp $(BIN)/deviceport.exe $(DIST)/deviceport-windows-amd64.exe

build-android:
	mkdir -p $(BIN)
	GOOS=android GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport-android-arm64 ./cmd/deviceport

# Populate dist/ with the release assets tracked by git.
release: build build-windows android-apk
	cp bin/DevicePort-$(VERSION).apk $(DIST)/DevicePort-$(VERSION).apk
	ls -lh $(DIST)

android-apk:
	bash android/build.sh

desktop:
	cd desktop && npm install && npm run start

clean:
	rm -rf $(BIN) android/build android/*.jks desktop/dist desktop/out
	$(GO) clean ./... 2>/dev/null || true
