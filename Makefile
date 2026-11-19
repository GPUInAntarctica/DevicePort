# DevicePort v0.1 build helpers
GO ?= go
VERSION := v0.1
BIN := bin

.PHONY: all build build-windows build-android test fmt vet clean desktop android-apk

all: fmt vet test build

fmt:
	$(GO) fmt ./...

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

build:
	mkdir -p $(BIN)
	CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport ./cmd/deviceport
	CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport-server ./cmd/deviceport-server
	CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceportctl ./cmd/deviceportctl

build-windows:
	mkdir -p $(BIN)
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport.exe ./cmd/deviceport
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport-server.exe ./cmd/deviceport-server
	GOOS=windows GOARCH=amd64 CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceportctl.exe ./cmd/deviceportctl

build-android:
	mkdir -p $(BIN)
	GOOS=android GOARCH=arm64 CGO_ENABLED=0 $(GO) build -o $(BIN)/deviceport-android-arm64 ./cmd/deviceport

build-all: build build-windows build-android

# Full Android APK (requires Android SDK/NDK + gomobile)
android-apk:
	bash android/build.sh

# Electron desktop app
desktop:
	cd desktop && npm install && npm run start

clean:
	rm -rf $(BIN) android/build android/*.jks desktop/dist desktop/out
	$(GO) clean ./... 2>/dev/null || true
