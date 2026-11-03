FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/playground ./cmd/playground

FROM ghcr.io/montferret/chromium:156.0.8078.80 AS api
USER 0:0
LABEL org.opencontainers.image.source="https://github.com/MontFerret/playground" \
      org.opencontainers.image.title="Ferret Playground API" \
      org.opencontainers.image.description="Ferret Playground API with isolated Chromium" \
      org.opencontainers.image.version="devel"
RUN apt-get update -qq && apt-get install -y --no-install-recommends nftables tini && rm -rf /var/lib/apt/lists/*
COPY deploy/browser/policy.json /etc/opt/chrome/policies/managed/playground.json
COPY deploy/browser/policy.json /etc/chromium/policies/managed/playground.json
COPY --from=build /out/playground /usr/bin/playground
EXPOSE 8080
HEALTHCHECK --interval=2s --timeout=3s --retries=20 CMD ["/usr/bin/playground","container","health"]
ENTRYPOINT ["/usr/bin/tini","--","/usr/bin/playground","container"]
