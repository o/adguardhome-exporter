FROM golang:1.26.1-bookworm AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w -X main.version=container" -o /out/adguardhome-exporter ./cmd/adguardhome-exporter

FROM gcr.io/distroless/static-debian13:nonroot

COPY --from=build /out/adguardhome-exporter /adguardhome-exporter
USER nonroot:nonroot
EXPOSE 9618
ENTRYPOINT ["/adguardhome-exporter"]
