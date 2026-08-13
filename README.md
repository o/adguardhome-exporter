# AdGuard Home Exporter

A lightweight Prometheus exporter for one or more AdGuard Home instances.
It is written in Go, uses scrape-time collection, and runs as a non-root
distroless container.

## Quick start

Create `config.yaml`:

```yaml
targets:
  - name: home
    url: http://adguardhome:3000
    username: admin
    password_file: /run/secrets/adguard_password
```

Run it:

```sh
docker run --rm -p 9618:9618 \
  -v "$PWD/config.yaml:/config.yaml:ro" \
  -v "$PWD/adguard_password:/run/secrets/adguard_password:ro" \
  ghcr.io/o/adguardhome-exporter:latest --config.file=/config.yaml
```

Prometheus can scrape `http://exporter:9618/metrics`.

## Configuration

Use either YAML or indexed environment variables.  They cannot be combined.
For environment configuration, define contiguous target indexes:

```yaml
ADGUARD_TARGET_1_NAME=home
ADGUARD_TARGET_1_URL=http://adguardhome:3000
ADGUARD_TARGET_1_USERNAME=admin
ADGUARD_TARGET_1_PASSWORD_FILE=/run/secrets/adguard_password
```

`USERNAME` and `PASSWORD` intentionally support direct values for trusted
homelabs.  Prefer `USERNAME_FILE` and `PASSWORD_FILE` when secrets are
available.  Values and files can be mixed per field, but direct and file forms
for the same field are rejected.

Optional exporter settings are `ADGUARD_EXPORTER_LISTEN_ADDRESS`,
`ADGUARD_EXPORTER_TIMEOUT`, `ADGUARD_EXPORTER_TOP_ENABLED`, and
`ADGUARD_EXPORTER_TOP_LIMIT`.

Top domain and client metrics are disabled by default because they can reveal
browsing and device information.  Enable them explicitly with
`collectors.top_enabled: true` or `ADGUARD_EXPORTER_TOP_ENABLED=true`.

## Metric semantics

AdGuard Home statistics are values for its current retention window, not
monotonic process counters.  Metrics such as `adguardhome_dns_queries` are
therefore Prometheus gauges and may naturally decrease when that window rolls
over.  The exporter never reconstructs counters in memory.

The default collection set covers server health, rolling DNS statistics,
upstream performance, statistics retention, filter list totals, and DHCP lease
counts.  It does not read query logs, client details, or TLS configuration.

See the included [Compose example](compose.yaml) and Grafana dashboard in
`dashboards/adguardhome-exporter.json`.
