FROM golang:1.27.2-bookworm@sha256:5cf287a799e6b94384bad13d16b14904c531f51ba65792237e122ce42b392f61 AS build

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
