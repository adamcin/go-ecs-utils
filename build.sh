#!/bin/bash
readonly BASEDIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
pushd "$BASEDIR"
export GOPATH="${BASEDIR}/gopath"
go get -u github.com/aws/aws-sdk-go-v2
go get -u github.com/rickar/props
go get -u github.com/jmespath/go-jmespath
go get -u github.com/go-ini/ini
go get -u github.com/hashicorp/golang-lru
go get -u gopkg.in/yaml.v2

modules=(javadock ssmple overrun)
for module in "${modules[@]}"; do
  pushd "$module"
  go fmt
  go build
  popd
done

