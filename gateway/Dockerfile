FROM golang:1.23-alpine AS builder

WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY *.go ./
COPY core/ core/

ARG IMAGE_REF=unknown
RUN CGO_ENABLED=0 go build -ldflags="-s -w -X 'github.com/DictionLabs/Diction/gateway/core.ImageRef=${IMAGE_REF}'" -o gateway .

FROM alpine:latest

RUN apk add --no-cache ca-certificates ffmpeg
COPY --from=builder /build/gateway /usr/local/bin/gateway

EXPOSE 8080

ENTRYPOINT ["gateway"]
