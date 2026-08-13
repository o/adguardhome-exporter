// Package adguard contains a small client for the read-only AdGuard Home API.
package adguard

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/o/adguardhome-exporter/internal/config"
)

const maxResponseSize = 4 << 20 // 4 MiB.

type Client struct {
	baseURL    string
	username   string
	password   string
	httpClient *http.Client
}

type Status struct {
	Version                    string  `json:"version"`
	ProtectionEnabled          bool    `json:"protection_enabled"`
	ProtectionDisabledDuration float64 `json:"protection_disabled_duration"`
	DHCPAvailable              bool    `json:"dhcp_available"`
	Running                    bool    `json:"running"`
	StartTime                  float64 `json:"start_time"`
}

type Statistics struct {
	NumDNSQueries           float64    `json:"num_dns_queries"`
	NumBlockedFiltering     float64    `json:"num_blocked_filtering"`
	NumReplacedSafeBrowsing float64    `json:"num_replaced_safebrowsing"`
	NumReplacedSafeSearch   float64    `json:"num_replaced_safesearch"`
	NumReplacedParental     float64    `json:"num_replaced_parental"`
	AverageProcessingTime   float64    `json:"avg_processing_time"`
	TopQueriedDomains       []TopEntry `json:"top_queried_domains"`
	TopBlockedDomains       []TopEntry `json:"top_blocked_domains"`
	TopClients              []TopEntry `json:"top_clients"`
	TopUpstreamResponses    []TopEntry `json:"top_upstreams_responses"`
	TopUpstreamAverageTime  []TopEntry `json:"top_upstreams_avg_time"`
}

type TopEntry map[string]float64

type StatisticsConfig struct {
	Enabled  bool    `json:"enabled"`
	Interval float64 `json:"interval"`
}

type FilterStatus struct {
	Enabled          bool     `json:"enabled"`
	Filters          []Filter `json:"filters"`
	WhitelistFilters []Filter `json:"whitelist_filters"`
	UserRules        []string `json:"user_rules"`
}

type Filter struct {
	Enabled    bool    `json:"enabled"`
	RulesCount float64 `json:"rules_count"`
}

type DHCPStatus struct {
	Enabled      bool          `json:"enabled"`
	Leases       []interface{} `json:"leases"`
	StaticLeases []interface{} `json:"static_leases"`
}

// Collection contains one read-only response from each API endpoint.
// A failed endpoint leaves its response at the zero value and records an error.
type Collection struct {
	Status                Status
	StatusError           error
	Statistics            Statistics
	StatisticsError       error
	StatisticsConfig      StatisticsConfig
	StatisticsConfigError error
	FilterStatus          FilterStatus
	FilterError           error
	DHCPStatus            DHCPStatus
	DHCPError             error
}

type CollectorResult struct {
	Name string
	Err  error
}

func NewClient(target config.Target, timeout time.Duration) (*Client, error) {
	tlsConfig, err := config.TLSConfig(target)
	if err != nil {
		return nil, fmt.Errorf("create TLS configuration: %w", err)
	}

	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.TLSClientConfig = tlsConfig

	return &Client{
		baseURL:  strings.TrimRight(target.URL, "/"),
		username: target.Username,
		password: target.Password,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
	}, nil
}

func (client *Client) Status(ctx context.Context) (Status, error) {
	var result Status
	err := client.get(ctx, "/control/status", &result)
	return result, err
}

func (client *Client) Statistics(ctx context.Context) (Statistics, error) {
	var result Statistics
	err := client.get(ctx, "/control/stats", &result)
	return result, err
}

func (client *Client) StatisticsConfig(ctx context.Context) (StatisticsConfig, error) {
	var result StatisticsConfig
	err := client.get(ctx, "/control/stats/config", &result)
	return result, err
}

func (client *Client) FilterStatus(ctx context.Context) (FilterStatus, error) {
	var result FilterStatus
	err := client.get(ctx, "/control/filtering/status", &result)
	return result, err
}

func (client *Client) DHCPStatus(ctx context.Context) (DHCPStatus, error) {
	var result DHCPStatus
	err := client.get(ctx, "/control/dhcp/status", &result)
	return result, err
}

// Collect gets all independent endpoints concurrently.  Every request shares
// the caller's context, so the target timeout applies to the whole collection.
func (client *Client) Collect(ctx context.Context) Collection {
	var collection Collection
	var waitGroup sync.WaitGroup

	waitGroup.Add(5)
	go func() {
		defer waitGroup.Done()
		collection.Status, collection.StatusError = client.Status(ctx)
	}()
	go func() {
		defer waitGroup.Done()
		collection.Statistics, collection.StatisticsError = client.Statistics(ctx)
	}()
	go func() {
		defer waitGroup.Done()
		collection.StatisticsConfig, collection.StatisticsConfigError = client.StatisticsConfig(ctx)
	}()
	go func() {
		defer waitGroup.Done()
		collection.FilterStatus, collection.FilterError = client.FilterStatus(ctx)
	}()
	go func() {
		defer waitGroup.Done()
		collection.DHCPStatus, collection.DHCPError = client.DHCPStatus(ctx)
	}()

	waitGroup.Wait()

	return collection
}

func (collection Collection) Results() []CollectorResult {
	return []CollectorResult{
		{Name: "status", Err: collection.StatusError},
		{Name: "statistics", Err: collection.StatisticsError},
		{Name: "statistics_config", Err: collection.StatisticsConfigError},
		{Name: "filtering", Err: collection.FilterError},
		{Name: "dhcp", Err: collection.DHCPError},
	}
}

func (client *Client) get(ctx context.Context, path string, destination interface{}) error {
	requestURL, err := url.JoinPath(client.baseURL, path)
	if err != nil {
		return fmt.Errorf("build request URL: %w", err)
	}

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	request.Header.Set("Accept", "application/json")
	if client.username != "" {
		request.SetBasicAuth(client.username, client.password)
	}

	response, err := client.httpClient.Do(request)
	if err != nil {
		return fmt.Errorf("request %s: %w", path, err)
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return fmt.Errorf("request %s: unexpected HTTP status %s", path, response.Status)
	}

	decoder := json.NewDecoder(io.LimitReader(response.Body, maxResponseSize))
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("decode %s response: %w", path, err)
	}

	return nil
}

func CleanLabel(value string) string {
	parsed, err := url.Parse(value)
	if err == nil && parsed.Scheme != "" && parsed.Host != "" {
		parsed.User = nil
		parsed.RawQuery = ""
		parsed.Fragment = ""
		return parsed.String()
	}
	return value
}
