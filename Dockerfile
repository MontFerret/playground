FROM --platform=$BUILDPLATFORM golang:1.27.3-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/playground ./cmd/playground && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/egress ./cmd/egress && \
    CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -o /out/browsercheck ./cmd/browsercheck

FROM alpine:3.23 AS egress
COPY --from=build /out/egress /usr/bin/egress
USER 65532:65532
ENTRYPOINT ["/usr/bin/egress"]

FROM alpine:3.23 AS api
COPY --from=build /out/playground /usr/bin/playground
USER 65532:65532
EXPOSE 8080
ENTRYPOINT ["/usr/bin/playground"]

FROM ghcr.io/montferret/chromium@sha256:08afea9e2d64b281c4b7e361d8349ac8c2685bee537e947639da66b858def763 AS relay
COPY --from=build /out/browsercheck /usr/bin/browsercheck
USER 10002:10002
