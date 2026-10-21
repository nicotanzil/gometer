# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

WORKDIR /src
ARG TARGETOS=linux
ARG TARGETARCH=amd64
COPY . .

ARG VERSION=0.6.0-dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X gometer/internal/cli.Version=${VERSION}" \
    -o /out/gometer ./cmd/gometer

FROM scratch
COPY --from=build /out/gometer /gometer
ENTRYPOINT ["/gometer"]
