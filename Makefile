export MAKEFLAGS='--silent --environment-override'

ROOT := $(abspath $(dir $(lastword $(MAKEFILE_LIST))))

.ONESHELL:

.PHONY: build
build:
	if [ ! -f $(ROOT)/env.yaml ]; then
		echo "Error: env.yaml not found. Create it with your PHP CGO flags."
		echo "See README.md for details."
		exit 1
	fi

	cd $(ROOT)
	CGO_ENABLED=1 go build -tags nowatcher -o dist/frankenasync .
	echo "Built dist/frankenasync"

.PHONY: run
run: build
	cd $(ROOT) && ./dist/frankenasync

.PHONY: test
test:
	cd $(ROOT) && go test ./asynctask/

.PHONY: bench
bench: build
	cd $(ROOT) && ./bench.sh

.PHONY: clean
clean:
	rm -rf dist/frankenasync

.PHONY: tidy
tidy:
	cd $(ROOT) && go mod tidy
