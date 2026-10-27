# The builder runs on the CI host's own arch and cross-compiles: Go needs no emulation,
# so an arm64 image costs seconds instead of a QEMU-emulated build.
FROM --platform=$BUILDPLATFORM golang:1.23-alpine AS builder
ARG TARGETOS TARGETARCH

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY core/ core/
COPY pairing/ pairing/

ARG IMAGE_REF=unknown
# VERSION is what GET /health reports, and it is what the app compares against its own
# version to decide whether to tell a self-hoster their gateway is behind. It defaults to
# "dev" on purpose: the app parses only a clean vX.Y[.Z], so "dev" is an explicit "no
# opinion" that shows no notice. That is the right answer for an untagged build, and the
# wrong one for a release -- hence release.yml passes the tag in.
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -ldflags="-s -w -X 'github.com/DictionLabs/Diction/gateway/core.ImageRef=${IMAGE_REF}' -X 'github.com/DictionLabs/Diction/gateway/core.Version=${VERSION}'" -o gateway .

FROM alpine:latest

RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=builder /build/gateway /usr/local/bin/gateway

EXPOSE 8080

ENTRYPOINT ["gateway"]
