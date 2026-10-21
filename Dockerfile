# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.27.3-bookworm AS build

WORKDIR /src
ARG TARGETOS
ARG TARGETARCH
COPY . .

ARG VERSION=0.8.0-dev
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build \
    -trimpath \
    -ldflags="-s -w -X gometer/internal/cli.Version=${VERSION}" \
    -o /out/gometer ./cmd/gometer

FROM scratch
LABEL org.opencontainers.image.source="https://github.com/nicotanzil/gometer"
COPY --from=build /out/gometer /gometer
ENTRYPOINT ["/gometer"]
