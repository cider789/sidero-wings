GIT_HEAD = $(shell git rev-parse HEAD | head -c8)

build:
	GOOS=linux GOARCH=amd64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_amd64 -v wings.go
	GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -gcflags "all=-trimpath=$(pwd)" -o build/wings_linux_arm64 -v wings.go

debug:
	go build -ldflags="-X github.com/pterodactyl/wings/system.Version=$(GIT_HEAD)"
	sudo ./wings --debug --ignore-certificate-errors --config config.yml --pprof --pprof-block-rate 1

# Runs a remotly debuggable session for Wings allowing an IDE to connect and target
# different breakpoints.
rmdebug:
	go build -gcflags "all=-N -l" -ldflags="-X github.com/pterodactyl/wings/system.Version=$(GIT_HEAD)" -race
	sudo dlv --listen=:2345 --headless=true --api-version=2 --accept-multiclient exec ./wings -- --debug --ignore-certificate-errors --config config.yml

cross-build: clean build compress

sidero-release-check:
	go test ./internal/sidero/... ./config ./router ./server ./environment/...
	go vet ./internal/sidero/... ./config
	CGO_ENABLED=0 go build -trimpath -o /tmp/sidero-wings-release-check wings.go

sidero-build:
	mkdir -p build
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags="-s -w" -o build/wings_sidero_linux_amd64 wings.go
	CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -ldflags="-s -w" -o build/wings_sidero_linux_arm64 wings.go

clean:
	rm -rf build/wings_*

.PHONY: all build compress clean sidero-release-check sidero-build
