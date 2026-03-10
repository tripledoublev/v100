FROM golang:1.25-bookworm

RUN apt-get update \
	&& DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
		ca-certificates \
		curl \
		git \
		patch \
		procps \
		ripgrep \
	&& ln -sf /usr/local/go/bin/go /usr/local/bin/go \
	&& ln -sf /usr/local/go/bin/gofmt /usr/local/bin/gofmt \
	&& rm -rf /var/lib/apt/lists/*

ENV PATH="/usr/local/go/bin:${PATH}"

WORKDIR /workspace

# The harness overrides HOME/GOCACHE/GOMODCACHE/GOPATH at exec time.
CMD ["sh", "-lc", "trap 'exit 0' TERM INT; while :; do sleep 3600; done"]
