PROJECT=forkdetector
ORGANISATION=wavesplatform
MODULE=github.com/$(ORGANISATION)/$(PROJECT)
SOURCE=$(shell find . -name '*.go' | grep -v vendor/)

VERSION=$(shell git describe --tags --always --dirty)

COZO_VERSION=0.7.6

COZO_LINUX_AMD64=x86_64-unknown-linux-gnu
COZO_LINUX_ARM64=aarch64-unknown-linux-gnu
COZO_DARWIN_AMD64=x86_64-apple-darwin
COZO_DARWIN_ARM64=aarch64-apple-darwin
COZO_WINDOWS_AMD64=x86_64-pc-windows-gnu

COZO_URL_LINUX_AMD64=https://github.com/cozodb/cozo/releases/download/v${COZO_VERSION}/libcozo_c-${COZO_VERSION}-${COZO_LINUX_AMD64}.a.gz
COZO_URL_LINUX_ARM64=https://github.com/cozodb/cozo/releases/download/v${COZO_VERSION}/libcozo_c-${COZO_VERSION}-${COZO_LINUX_ARM64}.a.gz
COZO_URL_APPLE_AMD64=https://github.com/cozodb/cozo/releases/download/v${COZO_VERSION}/libcozo_c-${COZO_VERSION}-${COZO_DARWIN_AMD64}.a.gz
COZO_URL_APPLE_ARM64=https://github.com/cozodb/cozo/releases/download/v${COZO_VERSION}/libcozo_c-${COZO_VERSION}-${COZO_DARWIN_ARM64}.a.gz
COZO_URL_WINDOWS_AMD64=https://github.com/cozodb/cozo/releases/download/v${COZO_VERSION}/libcozo_c-${COZO_VERSION}-${COZO_WINDOWS_AMD64}.a.gz

export GO111MODULE=on

.PHONY: vendor vetcheck fmtcheck clean build gotest update-go-deps

all: vendor vet-check fmt-check go-test mod-clean

ci: vendor vet-check fmt-check release go-test-race-coverage mod-clean

ver:
	@echo Building version: $(VERSION)

go-test:
	go test -cover $$(go list ./...)

go-test-race-coverage:
	go test -timeout 20m -race -coverprofile=coverage.txt -covermode=atomic $$(go list ./...)

fmt-check:
	@gofmt -l -s . | grep ".*\.go"; if [ "$$?" = "0" ]; then exit 1; fi

mod-clean:
	go mod tidy

update-go-deps: mod-clean
	@echo ">> updating Go dependencies"
	@for m in $$(go list -mod=readonly -m -f '{{ if and (not .Indirect) (not .Main)}}{{.Path}}{{end}}' all); do \
		go get $$m; \
	done
	go mod tidy
ifneq (,$(wildcard vendor))
	go mod vendor
endif

get-libs:
	@rm -rdf libs

	@mkdir -p libs/${COZO_LINUX_AMD64}
	@curl -L ${COZO_URL_LINUX_AMD64} -o libs/${COZO_LINUX_AMD64}/libcozo_c.a.gz
	@gunzip -f libs/${COZO_LINUX_AMD64}/libcozo_c.a.gz

	@mkdir -p libs/${COZO_LINUX_ARM64}
	@curl -L ${COZO_URL_LINUX_ARM64} -o libs/${COZO_LINUX_ARM64}/libcozo_c.a.gz
	@gunzip -f libs/${COZO_LINUX_ARM64}/libcozo_c.a.gz

	@mkdir -p libs/${COZO_DARWIN_AMD64}
	@curl -L ${COZO_URL_APPLE_AMD64} -o libs/${COZO_DARWIN_AMD64}/libcozo_c.a.gz
	@gunzip -f libs/${COZO_DARWIN_AMD64}/libcozo_c.a.gz

	@mkdir -p libs/${COZO_DARWIN_ARM64}
	@curl -L ${COZO_URL_APPLE_ARM64} -o libs/${COZO_DARWIN_ARM64}/libcozo_c.a.gz
	@gunzip -f libs/${COZO_DARWIN_ARM64}/libcozo_c.a.gz

	@mkdir -p libs/${COZO_WINDOWS_AMD64}
	@curl -L ${COZO_URL_WINDOWS_AMD64} -o libs/${COZO_WINDOWS_AMD64}/libcozo_c.a.gz
	@gunzip -f libs/${COZO_WINDOWS_AMD64}/libcozo_c.a.gz

clean:
	@rm -rf build
	go mod tidy

vendor:
	go mod vendor

vet-check:
	go vet ./...
	golangci-lint run -c .golangci-strict.yml

build-linux-amd64:
	@CGO_ENABLED=1 @CGO_LDFLAGS="-L${PWD}/libs/${COZO_LINUX_AMD64/}" GOOS=linux GOARCH=amd64 go build -o build/bin/linux-amd64/forkdetector -ldflags="-X main.version=$(VERSION)" .
build-linux-arm64:
	@CGO_ENABLED=1 @CGO_LDFLAGS="-L${PWD}/libs/${COZO_LINUX_ARM64}" GOOS=linux GOARCH=arm64 go build -o build/bin/linux-arm64/forkdetector -ldflags="-X main.version=$(VERSION)" .
build-darwin-amd64:
	@CGO_ENABLED=1 CGO_LDFLAGS="-L${PWD}/libs/${COZO_DARWIN_AMD64}" GOOS=darwin GOARCH=amd64 go build -o build/bin/darwin-amd64/forkdetector -ldflags="-X main.version=$(VERSION)" .
build-darwin-arm64:
	@CGO_ENABLED=1 CGO_LDFLAGS="-L${PWD}/libs/${COZO_DARWIN_ARM64}" GOOS=darwin GOARCH=arm64 go build -o build/bin/darwin-arm64/forkdetector -ldflags="-X main.version=$(VERSION)" .
build-windows-amd64:
	@CGO_ENABLED=1 CGO_LDFLAGS="-L${PWD}/libs/${COZO_WINDOWS_AMD64}" GOOS=windows GOARCH=amd64 go build -o build/bin/windows-amd64/forkdetector.exe -ldflags="main.version=$(VERSION)" .

release: ver build-linux-amd64 build-linux-arm64 build-darwin-amd64 build-windows-amd64

dist: clean release
	@mkdir -p build/dist
	@cd ./build/; zip -j ./dist/forkdetector_$(VERSION)_Windows-amd64.zip ./bin/windows-amd64/forkdetector*
	@cd ./build/bin/linux-amd64/; tar pzcvf ../../dist/forkdetector_$(VERSION)_Linux-amd64.tar.gz ./forkdetector*
	@cd ./build/bin/linux-arm64/; tar pzcvf ../../dist/forkdetector_$(VERSION)_Linux-arm64.tar.gz ./forkdetector*
	@cd ./build/bin/darwin-amd64/; tar pzcvf ../../dist/forkdetector_$(VERSION)_macOS-amd64.tar.gz ./forkdetector*
	@cd ./build/bin/darwin-arm64/; tar pzcvf ../../dist/forkdetector_$(VERSION)_macOS-arm64.tar.gz ./forkdetector*

