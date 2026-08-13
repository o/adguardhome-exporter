# AdGuard Home Exporter

A small Prometheus exporter for AdGuard Home. It supports multiple targets,
collects data during the Prometheus scrape, and is shipped as a distroless Go
container.

### What it collects

* AdGuard Home status, version, protection state, and uptime
* DNS query, blocking, and processing-time statistics
* Upstream response count and average response time
* Filter list, rule, DHCP, and statistics-retention state

Top domains and clients are disabled by default. They can reveal browsing and
device information.

### Running with Docker Compose

Download the Compose file:

    curl -fsSLO https://raw.githubusercontent.com/o/adguardhome-exporter/main/compose.yaml

Create the password file next to it:

    printf '%s\n' 'password' > adguard_password.txt

Update `ADGUARD_TARGET_1_URL` and `ADGUARD_TARGET_1_USERNAME` in the
downloaded `compose.yaml`, then run:

    docker compose up -d

Check it:

    curl http://127.0.0.1:9618/metrics

The Compose file uses Docker secrets, a read-only filesystem, no Linux
capabilities, and a health check.

### Running with Docker

Create a config file:

    # config.yaml
    targets:
      - name: home
        url: http://adguardhome:3000
        username: admin
        password_file: /run/secrets/adguard_password

Run the exporter:

    docker run --rm -p 9618:9618 \
      -v "$PWD/config.yaml:/config.yaml:ro" \
      -v "$PWD/adguard_password:/run/secrets/adguard_password:ro" \
      ghcr.io/o/adguardhome-exporter:latest --config.file=/config.yaml

Prometheus should scrape `http://exporter:9618/metrics`.

### Configuration

The exporter accepts one configuration source: YAML or environment variables.
Do not use both target formats together.

##### YAML

    web:
      listen_address: ":9618"
      timeout: 5s
    collectors:
      top_enabled: false
      top_limit: 10
    targets:
      - name: home
        url: http://adguardhome:3000
        username_file: /run/secrets/adguard_username
        password_file: /run/secrets/adguard_password
      - name: remote
        url: https://adguard.example.com
        username: admin
        password: password

`username` and `password` are supported for simple homelab setups. Use
`username_file` and `password_file` for Docker or Kubernetes secrets. You can
mix direct values and files, but not for the same field.

##### Environment variables

Targets use contiguous indexes. The following is the same configuration as the
first YAML target above:

    ADGUARD_TARGET_1_NAME=home
    ADGUARD_TARGET_1_URL=http://adguardhome:3000
    ADGUARD_TARGET_1_USERNAME_FILE=/run/secrets/adguard_username
    ADGUARD_TARGET_1_PASSWORD_FILE=/run/secrets/adguard_password

Optional exporter variables:

    ADGUARD_EXPORTER_LISTEN_ADDRESS=:9618
    ADGUARD_EXPORTER_TIMEOUT=5s
    ADGUARD_EXPORTER_TOP_ENABLED=false
    ADGUARD_EXPORTER_TOP_LIMIT=10

### Metrics

AdGuard Home returns statistics for its current retention window. Those values
are gauges, not counters, and they may decrease when AdGuard rotates its
statistics.

Useful metrics:

    adguardhome_up
    adguardhome_dns_queries
    adguardhome_dns_queries_blocked
    adguardhome_dns_query_processing_duration_seconds
    adguardhome_upstream_response_duration_seconds
    adguardhome_filtering_enabled
    adguardhome_dhcp_leases

The `instance` label is the target `name` from the configuration. The exporter
does not read query logs, client details, or TLS configuration.

### Development

Requires Go 1.26 and Docker for container testing.

    go test ./...
    go test -race ./...
    go vet ./...
    go build ./cmd/adguardhome-exporter

Run against a local AdGuard Home instance:

    ADGUARD_TARGET_1_NAME=local \
    ADGUARD_TARGET_1_URL=http://127.0.0.1:3000 \
    ADGUARD_TARGET_1_USERNAME=admin \
    ADGUARD_TARGET_1_PASSWORD=password \
    go run ./cmd/adguardhome-exporter

Then inspect the output:

    curl http://127.0.0.1:9618/metrics

The Grafana dashboard is in `dashboards/adguardhome-exporter.json`.

### License

[MIT](LICENSE)
