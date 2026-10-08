FROM golang:1.27.0-bookworm@sha256:ded31c68586d2e49e760acc2e65a884b23d032e9bbbed0ae0c55abd3fcaf4452 AS build

ARG VERSION=dev
ARG REVISION=unknown

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.version=$VERSION -X main.revision=$REVISION" \
    -o /out/adguardhome-exporter ./cmd/adguardhome-exporter

FROM gcr.io/distroless/static-debian13:nonroot@sha256:f7f8f729987ad0fdf6b05eeeae94b26e6a0f613bdf46feea7fc40f7bd72953e6

ARG VERSION=dev
ARG REVISION=unknown

LABEL org.opencontainers.image.title="AdGuard Home Exporter" \
      org.opencontainers.image.description="A small Prometheus exporter for AdGuard Home." \
      org.opencontainers.image.source="https://github.com/o/adguardhome-exporter" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="$VERSION" \
      org.opencontainers.image.revision="$REVISION"

COPY --from=build /out/adguardhome-exporter /adguardhome-exporter
USER nonroot:nonroot
EXPOSE 9618
ENTRYPOINT ["/adguardhome-exporter"]
