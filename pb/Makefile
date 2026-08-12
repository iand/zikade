# Regenerates msg.pb.go from msg.proto.
#
# msg.proto imports record.proto from go-libp2p-record. Rather than cloning that
# repository at a hardcoded version, the proto is staged from the module cache so
# it always matches the version in go.mod.
#
# Requires protoc and protoc-gen-go. Install the generator at the same version as
# the google.golang.org/protobuf runtime in go.mod:
#
#	go install google.golang.org/protobuf/cmd/protoc-gen-go@$(shell go list -m -f '{{.Version}}' google.golang.org/protobuf)

RECORD_DIR := $(shell go list -m -f '{{.Dir}}' github.com/libp2p/go-libp2p-record)
STAGE      := .protoinc
RECORD_PKG := github.com/libp2p/go-libp2p-record/pb

all: reset build clean

stage:
	mkdir -p $(STAGE)/$(RECORD_PKG)
	cp $(RECORD_DIR)/pb/record.proto $(STAGE)/$(RECORD_PKG)/

build: stage
	protoc -I $(STAGE) -I . \
		--go_out=./ \
		--go_opt=M$(RECORD_PKG)/record.proto=$(RECORD_PKG) \
		./msg.proto

reset:
	rm -f msg.pb.go

clean:
	rm -rf $(STAGE)

.PHONY: all build stage reset clean
