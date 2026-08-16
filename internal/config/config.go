// Package config loads and validates exporter configuration.
package config

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"go.yaml.in/yaml/v3"
)

const (
	defaultListenAddress = ":9618"
	defaultTimeout       = 5 * time.Second
	defaultPollInterval  = 15 * time.Second
	defaultTopLimit      = 10
	maxTopLimit          = 100
)

type Config struct {
	Web        WebConfig        `yaml:"web"`
	Log        LogConfig        `yaml:"log"`
	Collectors CollectorsConfig `yaml:"collectors"`
	Targets    []Target         `yaml:"targets"`
}

type WebConfig struct {
	ListenAddress string        `yaml:"listen_address"`
	Timeout       time.Duration `yaml:"timeout"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type CollectorsConfig struct {
	PollInterval time.Duration `yaml:"poll_interval"`
	TopEnabled   bool          `yaml:"top_enabled"`
	TopLimit     int           `yaml:"top_limit"`
}

type Target struct {
	Name               string `yaml:"name"`
	URL                string `yaml:"url"`
	Username           string `yaml:"username"`
	UsernameFile       string `yaml:"username_file"`
	Password           string `yaml:"password"`
	PasswordFile       string `yaml:"password_file"`
	CAFile             string `yaml:"ca_file"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

func Default() Config {
	return Config{
		Web: WebConfig{
			ListenAddress: defaultListenAddress,
			Timeout:       defaultTimeout,
		},
		Log: LogConfig{
			Level:  "info",
			Format: "text",
		},
		Collectors: CollectorsConfig{PollInterval: defaultPollInterval, TopLimit: defaultTopLimit},
	}
}

// Load reads either a strict YAML file or indexed environment variables.
// Supplying both forms of target configuration is rejected.
func Load(configFile string) (Config, error) {
	if configFile != "" && hasTargetEnvironment() {
		return Config{}, errors.New("--config.file and ADGUARD_TARGET_* configuration are mutually exclusive")
	}

	if configFile != "" {
		return loadYAML(configFile)
	}

	return loadEnvironment()
}

func loadYAML(path string) (Config, error) {
	file, err := os.Open(filepath.Clean(path))
	if err != nil {
		return Config{}, fmt.Errorf("open config: %w", err)
	}
	defer file.Close()

	config := Default()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)

	if err := decoder.Decode(&config); err != nil {
		return Config{}, fmt.Errorf("decode config: %w", err)
	}

	if err := resolveAndValidate(&config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func loadEnvironment() (Config, error) {
	config := Default()

	if err := loadCommonEnvironment(&config); err != nil {
		return Config{}, err
	}

	for index := 1; ; index++ {
		target, found, err := loadEnvironmentTarget(index)
		if err != nil {
			return Config{}, err
		}
		if !found {
			break
		}

		config.Targets = append(config.Targets, target)
	}

	if err := resolveAndValidate(&config); err != nil {
		return Config{}, err
	}

	return config, nil
}

func loadCommonEnvironment(config *Config) error {
	config.Web.ListenAddress = environmentOrDefault("ADGUARD_EXPORTER_LISTEN_ADDRESS", config.Web.ListenAddress)
	config.Log.Level = environmentOrDefault("ADGUARD_EXPORTER_LOG_LEVEL", config.Log.Level)
	config.Log.Format = environmentOrDefault("ADGUARD_EXPORTER_LOG_FORMAT", config.Log.Format)

	if value := os.Getenv("ADGUARD_EXPORTER_TIMEOUT"); value != "" {
		timeout, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("ADGUARD_EXPORTER_TIMEOUT: %w", err)
		}
		config.Web.Timeout = timeout
	}

	if value := os.Getenv("ADGUARD_EXPORTER_TOP_ENABLED"); value != "" {
		enabled, err := strconv.ParseBool(value)
		if err != nil {
			return fmt.Errorf("ADGUARD_EXPORTER_TOP_ENABLED: %w", err)
		}
		config.Collectors.TopEnabled = enabled
	}
	if value := os.Getenv("ADGUARD_EXPORTER_POLL_INTERVAL"); value != "" {
		interval, err := time.ParseDuration(value)
		if err != nil {
			return fmt.Errorf("ADGUARD_EXPORTER_POLL_INTERVAL: %w", err)
		}
		config.Collectors.PollInterval = interval
	}

	if value := os.Getenv("ADGUARD_EXPORTER_TOP_LIMIT"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("ADGUARD_EXPORTER_TOP_LIMIT: %w", err)
		}
		config.Collectors.TopLimit = limit
	}

	return nil
}

func loadEnvironmentTarget(index int) (Target, bool, error) {
	prefix := fmt.Sprintf("ADGUARD_TARGET_%d_", index)
	name := os.Getenv(prefix + "NAME")
	targetURL := os.Getenv(prefix + "URL")

	if name == "" && targetURL == "" {
		if targetExistsAfter(index) {
			return Target{}, false, fmt.Errorf("target indexes must be contiguous; missing target %d", index)
		}
		return Target{}, false, nil
	}

	if name == "" || targetURL == "" {
		return Target{}, false, fmt.Errorf("target %d requires NAME and URL", index)
	}

	target := Target{
		Name:         name,
		URL:          targetURL,
		Username:     os.Getenv(prefix + "USERNAME"),
		UsernameFile: os.Getenv(prefix + "USERNAME_FILE"),
		Password:     os.Getenv(prefix + "PASSWORD"),
		PasswordFile: os.Getenv(prefix + "PASSWORD_FILE"),
		CAFile:       os.Getenv(prefix + "CA_FILE"),
	}

	if value := os.Getenv(prefix + "INSECURE_SKIP_VERIFY"); value != "" {
		skipVerification, err := strconv.ParseBool(value)
		if err != nil {
			return Target{}, false, fmt.Errorf("%sINSECURE_SKIP_VERIFY: %w", prefix, err)
		}
		target.InsecureSkipVerify = skipVerification
	}

	return target, true, nil
}

func resolveAndValidate(config *Config) error {
	if err := validateCommonConfig(*config); err != nil {
		return err
	}
	if len(config.Targets) == 0 {
		return errors.New("at least one target is required")
	}

	targetNames := make(map[string]struct{}, len(config.Targets))
	for index := range config.Targets {
		target := &config.Targets[index]
		if err := validateTarget(target, targetNames); err != nil {
			return err
		}
	}

	return nil
}

func validateCommonConfig(config Config) error {
	if config.Web.ListenAddress == "" {
		return errors.New("web.listen_address must not be empty")
	}
	if config.Web.Timeout <= 0 {
		return errors.New("web.timeout must be positive")
	}
	if config.Collectors.TopLimit < 1 || config.Collectors.TopLimit > maxTopLimit {
		return fmt.Errorf("collectors.top_limit must be between 1 and %d", maxTopLimit)
	}
	if config.Collectors.PollInterval <= 0 {
		return errors.New("collectors.poll_interval must be positive")
	}
	if !validLogLevel(config.Log.Level) {
		return errors.New("log.level must be debug, info, warn, or error")
	}
	if config.Log.Format != "text" && config.Log.Format != "json" {
		return errors.New("log.format must be text or json")
	}

	return nil
}

func validateTarget(target *Target, targetNames map[string]struct{}) error {
	if target.Name == "" {
		return errors.New("target name is required")
	}
	if _, exists := targetNames[target.Name]; exists {
		return fmt.Errorf("duplicate target name %q", target.Name)
	}
	targetNames[target.Name] = struct{}{}

	parsedURL, err := url.Parse(target.URL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" || parsedURL.User != nil {
		return fmt.Errorf("target %q has invalid URL", target.Name)
	}
	if parsedURL.Scheme != "http" && parsedURL.Scheme != "https" {
		return fmt.Errorf("target %q URL must use HTTP or HTTPS", target.Name)
	}
	target.URL = strings.TrimRight(target.URL, "/")

	if err := readSecret(&target.Username, &target.UsernameFile, "username", target.Name); err != nil {
		return err
	}
	if err := readSecret(&target.Password, &target.PasswordFile, "password", target.Name); err != nil {
		return err
	}
	if (target.Username == "") != (target.Password == "") {
		return fmt.Errorf("target %q must provide both username and password, or neither", target.Name)
	}

	if target.CAFile != "" {
		if _, err := os.ReadFile(filepath.Clean(target.CAFile)); err != nil {
			return fmt.Errorf("target %q ca_file: %w", target.Name, err)
		}
	}

	return nil
}

func readSecret(value, filePath *string, field, targetName string) error {
	if *value != "" && *filePath != "" {
		return fmt.Errorf("target %q specifies both %s and %s_file", targetName, field, field)
	}
	if *filePath == "" {
		return nil
	}

	contents, err := os.ReadFile(filepath.Clean(*filePath))
	if err != nil {
		return fmt.Errorf("target %q %s_file: %w", targetName, field, err)
	}

	*value = strings.TrimRight(string(contents), "\r\n")
	if *value == "" {
		return fmt.Errorf("target %q %s_file is empty", targetName, field)
	}
	*filePath = ""

	return nil
}

// TLSConfig returns the TLS configuration for a target.  A nil configuration
// uses Go's secure defaults.
func TLSConfig(target Target) (*tls.Config, error) {
	if target.CAFile == "" && !target.InsecureSkipVerify {
		return nil, nil
	}

	config := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: target.InsecureSkipVerify, // #nosec G402 -- an explicit, documented user option.
	}

	if target.CAFile == "" {
		return config, nil
	}

	contents, err := os.ReadFile(filepath.Clean(target.CAFile))
	if err != nil {
		return nil, fmt.Errorf("read CA file: %w", err)
	}

	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(contents) {
		return nil, errors.New("CA file contains no certificate")
	}
	config.RootCAs = pool

	return config, nil
}

func environmentOrDefault(key, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}

	return value
}

func hasTargetEnvironment() bool {
	for _, variable := range os.Environ() {
		if strings.HasPrefix(variable, "ADGUARD_TARGET_") {
			return true
		}
	}

	return false
}

func targetExistsAfter(currentIndex int) bool {
	for _, variable := range os.Environ() {
		key, _, _ := strings.Cut(variable, "=")
		index, found := targetIndex(key)
		if found && index > currentIndex {
			return true
		}
	}

	return false
}

func targetIndex(key string) (int, bool) {
	const prefix = "ADGUARD_TARGET_"
	if !strings.HasPrefix(key, prefix) {
		return 0, false
	}

	rest := strings.TrimPrefix(key, prefix)
	indexText, _, found := strings.Cut(rest, "_")
	if !found {
		return 0, false
	}

	index, err := strconv.Atoi(indexText)
	if err != nil {
		return 0, false
	}

	return index, true
}

func validLogLevel(level string) bool {
	return level == "debug" || level == "info" || level == "warn" || level == "error"
}
