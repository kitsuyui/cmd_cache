#!/bin/sh
go get -d ./...
CGO_ENABLE=0 \
gox \
-ldflags '-w -s' \
-ldflags '-X main.version='"$BUILD_VERSION" \
-output='build/cmd_cache_{{.OS}}_{{.Arch}}'
