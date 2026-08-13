// Package exporter translates AdGuard Home API responses into Prometheus metrics.
package exporter

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/o/adguardhome-exporter/internal/adguard"
	"github.com/o/adguardhome-exporter/internal/config"
	"github.com/prometheus/client_golang/prometheus"
)

type Exporter struct {
	clients     []targetClient
	topEnabled  bool
	topLimit    int
	timeout     time.Duration
	logger      *slog.Logger
	descriptors descriptors
}

type targetClient struct {
	name   string
	client *adguard.Client
}

type descriptors struct {
	up                 *prometheus.Desc
	scrapeDuration     *prometheus.Desc
	collectorSuccess   *prometheus.Desc
	info               *prometheus.Desc
	running            *prometheus.Desc
	protectionEnabled  *prometheus.Desc
	protectionDisabled *prometheus.Desc
	dhcpAvailable      *prometheus.Desc
	startTime          *prometheus.Desc
	statisticsEnabled  *prometheus.Desc
	statisticsWindow   *prometheus.Desc
	dnsQueries         *prometheus.Desc
	dnsBlocked         *prometheus.Desc
	dnsReplaced        *prometheus.Desc
	processingTime     *prometheus.Desc
	upstreamResponses  *prometheus.Desc
	upstreamTime       *prometheus.Desc
	filteringEnabled   *prometheus.Desc
	filterLists        *prometheus.Desc
	filterRules        *prometheus.Desc
	userRules          *prometheus.Desc
	dhcpEnabled        *prometheus.Desc
	dhcpLeases         *prometheus.Desc
	topDomains         *prometheus.Desc
	topBlockedDomains  *prometheus.Desc
	topClients         *prometheus.Desc
}

func New(cfg config.Config, logger *slog.Logger) (*Exporter, error) {
	exporter := &Exporter{
		topEnabled:  cfg.Collectors.TopEnabled,
		topLimit:    cfg.Collectors.TopLimit,
		timeout:     cfg.Web.Timeout,
		logger:      logger,
		descriptors: newDescriptors(),
	}

	for _, target := range cfg.Targets {
		client, err := adguard.NewClient(target, cfg.Web.Timeout)
		if err != nil {
			return nil, err
		}
		exporter.clients = append(exporter.clients, targetClient{name: target.Name, client: client})
	}

	return exporter, nil
}

func (exporter *Exporter) Describe(channel chan<- *prometheus.Desc) {
	for _, descriptor := range exporter.descriptors.all() {
		channel <- descriptor
	}
}

func (exporter *Exporter) Collect(channel chan<- prometheus.Metric) {
	var waitGroup sync.WaitGroup
	for _, target := range exporter.clients {
		waitGroup.Add(1)
		go func(target targetClient) {
			defer waitGroup.Done()
			exporter.collectTarget(target, channel)
		}(target)
	}
	waitGroup.Wait()
}

func (exporter *Exporter) collectTarget(target targetClient, channel chan<- prometheus.Metric) {
	started := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), exporter.timeout)
	defer cancel()

	data := target.client.Collect(ctx)

	for _, result := range data.Results() {
		success := 1.0
		if result.Err != nil {
			success = 0
			exporter.logger.Warn("AdGuard Home request failed", "target", target.name, "collector", result.Name, "error", result.Err)
		}
		emit(channel, exporter.descriptors.collectorSuccess, prometheus.GaugeValue, success, target.name, result.Name)
	}

	up := 0.0
	if data.StatusError == nil {
		up = 1
	}
	emit(channel, exporter.descriptors.up, prometheus.GaugeValue, up, target.name)
	emit(channel, exporter.descriptors.scrapeDuration, prometheus.GaugeValue, time.Since(started).Seconds(), target.name)

	if data.StatusError == nil {
		emitStatus(channel, exporter.descriptors, target.name, data.Status)
	}
	if data.StatisticsError == nil {
		emitStatistics(channel, exporter.descriptors, target.name, data.Statistics, exporter.topEnabled, exporter.topLimit)
	}
	if data.StatisticsConfigError == nil {
		emit(channel, exporter.descriptors.statisticsEnabled, prometheus.GaugeValue, boolValue(data.StatisticsConfig.Enabled), target.name)
		emit(channel, exporter.descriptors.statisticsWindow, prometheus.GaugeValue, data.StatisticsConfig.Interval/1000, target.name)
	}
	if data.FilterError == nil {
		emitFiltering(channel, exporter.descriptors, target.name, data.FilterStatus)
	}
	if data.DHCPError == nil {
		emit(channel, exporter.descriptors.dhcpEnabled, prometheus.GaugeValue, boolValue(data.DHCPStatus.Enabled), target.name)
		emit(channel, exporter.descriptors.dhcpLeases, prometheus.GaugeValue, float64(len(data.DHCPStatus.Leases)), target.name, "dynamic")
		emit(channel, exporter.descriptors.dhcpLeases, prometheus.GaugeValue, float64(len(data.DHCPStatus.StaticLeases)), target.name, "static")
	}
}

func emitStatus(channel chan<- prometheus.Metric, d descriptors, name string, status adguard.Status) {
	emit(channel, d.info, prometheus.GaugeValue, 1, name, status.Version)
	emit(channel, d.running, prometheus.GaugeValue, boolValue(status.Running), name)
	emit(channel, d.protectionEnabled, prometheus.GaugeValue, boolValue(status.ProtectionEnabled), name)
	emit(channel, d.protectionDisabled, prometheus.GaugeValue, status.ProtectionDisabledDuration, name)
	emit(channel, d.dhcpAvailable, prometheus.GaugeValue, boolValue(status.DHCPAvailable), name)
	emit(channel, d.startTime, prometheus.GaugeValue, status.StartTime/1000, name)
}

func emitStatistics(channel chan<- prometheus.Metric, d descriptors, name string, statistics adguard.Statistics, topEnabled bool, topLimit int) {
	emit(channel, d.dnsQueries, prometheus.GaugeValue, statistics.NumDNSQueries, name)
	emit(channel, d.dnsBlocked, prometheus.GaugeValue, statistics.NumBlockedFiltering, name)
	emit(channel, d.dnsReplaced, prometheus.GaugeValue, statistics.NumReplacedSafeBrowsing, name, "safe_browsing")
	emit(channel, d.dnsReplaced, prometheus.GaugeValue, statistics.NumReplacedSafeSearch, name, "safe_search")
	emit(channel, d.dnsReplaced, prometheus.GaugeValue, statistics.NumReplacedParental, name, "parental")
	emit(channel, d.processingTime, prometheus.GaugeValue, statistics.AverageProcessingTime, name)
	emitTopEntries(channel, d.upstreamResponses, name, statistics.TopUpstreamResponses, "upstream", 100, true)
	emitTopEntries(channel, d.upstreamTime, name, statistics.TopUpstreamAverageTime, "upstream", 100, true)
	if !topEnabled {
		return
	}
	emitTopEntries(channel, d.topDomains, name, statistics.TopQueriedDomains, "domain", topLimit, false)
	emitTopEntries(channel, d.topBlockedDomains, name, statistics.TopBlockedDomains, "domain", topLimit, false)
	emitTopEntries(channel, d.topClients, name, statistics.TopClients, "client", topLimit, false)
}

func emitFiltering(channel chan<- prometheus.Metric, d descriptors, name string, status adguard.FilterStatus) {
	emit(channel, d.filteringEnabled, prometheus.GaugeValue, boolValue(status.Enabled), name)
	emit(channel, d.userRules, prometheus.GaugeValue, float64(len(status.UserRules)), name)
	emitFilterEntries(channel, d, name, "block", status.Filters)
	emitFilterEntries(channel, d, name, "allow", status.WhitelistFilters)
}

func emitFilterEntries(channel chan<- prometheus.Metric, d descriptors, name, filterType string, filters []adguard.Filter) {
	counts := map[bool]float64{}
	rules := map[bool]float64{}

	for _, filter := range filters {
		counts[filter.Enabled]++
		rules[filter.Enabled] += filter.RulesCount
	}

	for _, enabled := range []bool{false, true} {
		label := boolLabel(enabled)
		emit(channel, d.filterLists, prometheus.GaugeValue, counts[enabled], name, filterType, label)
		emit(channel, d.filterRules, prometheus.GaugeValue, rules[enabled], name, filterType, label)
	}
}

func emitTopEntries(channel chan<- prometheus.Metric, descriptor *prometheus.Desc, name string, entries []adguard.TopEntry, label string, limit int, clean bool) {
	emitted := 0
	for _, entry := range entries {
		for value, count := range entry {
			if emitted >= limit {
				return
			}
			if clean {
				value = adguard.CleanLabel(value)
			}
			emit(channel, descriptor, prometheus.GaugeValue, count, name, value)
			emitted++
		}
	}
}

func emit(channel chan<- prometheus.Metric, descriptor *prometheus.Desc, valueType prometheus.ValueType, value float64, labels ...string) {
	channel <- prometheus.MustNewConstMetric(descriptor, valueType, value, labels...)
}

func boolValue(value bool) float64 {
	if value {
		return 1
	}

	return 0
}

func boolLabel(value bool) string {
	if value {
		return "true"
	}

	return "false"
}

func newDescriptors() descriptors {
	metric := func(name, help string, labels ...string) *prometheus.Desc {
		return prometheus.NewDesc("adguardhome_"+name, help, labels, nil)
	}
	return descriptors{
		up: metric("up", "Whether every enabled AdGuard Home collector succeeded.", "instance"), scrapeDuration: metric("scrape_duration_seconds", "Time spent collecting one target.", "instance"), collectorSuccess: metric("collector_success", "Whether one AdGuard Home API collector succeeded.", "instance", "collector"),
		info: metric("info", "AdGuard Home version information.", "instance", "version"), running: metric("dns_running", "Whether the AdGuard Home DNS server is running.", "instance"), protectionEnabled: metric("protection_enabled", "Whether protection is enabled.", "instance"), protectionDisabled: metric("protection_disabled_duration_seconds", "Duration for which protection has been disabled.", "instance"), dhcpAvailable: metric("dhcp_available", "Whether built-in DHCP is available.", "instance"), startTime: metric("start_time_seconds", "AdGuard Home web API start time.", "instance"),
		statisticsEnabled: metric("statistics_enabled", "Whether AdGuard Home statistics are enabled.", "instance"), statisticsWindow: metric("statistics_retention_seconds", "AdGuard Home statistics retention window.", "instance"), dnsQueries: metric("dns_queries", "DNS queries in AdGuard Home's current statistics window.", "instance"), dnsBlocked: metric("dns_queries_blocked", "Blocked DNS queries in AdGuard Home's current statistics window.", "instance"), dnsReplaced: metric("dns_queries_replaced", "DNS queries replaced by a protection subsystem in the current statistics window.", "instance", "reason"), processingTime: metric("dns_query_processing_duration_seconds", "Average DNS query processing time in the current statistics window.", "instance"),
		upstreamResponses: metric("upstream_responses", "Responses from an upstream in the current statistics window.", "instance", "upstream"), upstreamTime: metric("upstream_response_duration_seconds", "Average response time from an upstream in the current statistics window.", "instance", "upstream"), filteringEnabled: metric("filtering_enabled", "Whether filtering is enabled.", "instance"), filterLists: metric("filter_lists", "Configured filter lists.", "instance", "type", "enabled"), filterRules: metric("filter_list_rules", "Rules in configured filter lists.", "instance", "type", "enabled"), userRules: metric("user_rules", "Configured user rules.", "instance"), dhcpEnabled: metric("dhcp_enabled", "Whether DHCP is enabled.", "instance"), dhcpLeases: metric("dhcp_leases", "DHCP leases.", "instance", "type"),
		topDomains: metric("top_queried_domain_queries", "Queries for a top queried domain in the current statistics window.", "instance", "domain"), topBlockedDomains: metric("top_blocked_domain_queries", "Queries for a top blocked domain in the current statistics window.", "instance", "domain"), topClients: metric("top_client_queries", "Queries from a top client in the current statistics window.", "instance", "client"),
	}
}

func (d descriptors) all() []*prometheus.Desc {
	return []*prometheus.Desc{
		d.up,
		d.scrapeDuration,
		d.collectorSuccess,
		d.info,
		d.running,
		d.protectionEnabled,
		d.protectionDisabled,
		d.dhcpAvailable,
		d.startTime,
		d.statisticsEnabled,
		d.statisticsWindow,
		d.dnsQueries,
		d.dnsBlocked,
		d.dnsReplaced,
		d.processingTime,
		d.upstreamResponses,
		d.upstreamTime,
		d.filteringEnabled,
		d.filterLists,
		d.filterRules,
		d.userRules,
		d.dhcpEnabled,
		d.dhcpLeases,
		d.topDomains,
		d.topBlockedDomains,
		d.topClients,
	}
}
