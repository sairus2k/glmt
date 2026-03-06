package auth

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// Credentials holds the GitLab host and token.
type Credentials struct {
	Host     string
	Token    string
	Protocol string // "https" or "http"
}

// glabHostConfig represents the per-host configuration in glab's config.
type glabHostConfig struct {
	Token       string `yaml:"token"`
	APIHost     string `yaml:"api_host"`
	GitProtocol string `yaml:"git_protocol"`
	APIProtocol string `yaml:"api_protocol"`
}

// ReadCredentials reads glab credentials from the config file.
// If host is empty, returns the first host found (in file order).
// If host is specified, returns credentials for that specific host.
// configDir is the path to the glab config directory (e.g., ~/.config/glab-cli).
func ReadCredentials(configDir string, host string) (*Credentials, error) {
	configPath := filepath.Join(configDir, "config.yml")

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("no glab config found at %s", configPath)
	}

	hosts, err := parseHosts(data, configPath)
	if err != nil {
		return nil, err
	}

	if len(hosts) == 0 {
		return nil, fmt.Errorf("no hosts configured")
	}

	if host != "" {
		for _, entry := range hosts {
			if entry.host == host {
				return &Credentials{
					Host:     host,
					Token:    entry.config.Token,
					Protocol: protocolOrDefault(entry.config.APIProtocol),
				}, nil
			}
		}

		return nil, fmt.Errorf("host %s not found", host)
	}

	// Return the first host in file order.
	first := hosts[0]

	return &Credentials{
		Host:     first.host,
		Token:    first.config.Token,
		Protocol: protocolOrDefault(first.config.APIProtocol),
	}, nil
}

// hostEntry pairs a hostname with its configuration, preserving file order.
type hostEntry struct {
	host   string
	config glabHostConfig
}

// parseHosts parses the glab config YAML, preserving the order of host entries.
func parseHosts(data []byte, configPath string) ([]hostEntry, error) {
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, fmt.Errorf("failed to parse glab config at %s: %w", configPath, err)
	}

	if root.Kind != yaml.DocumentNode || len(root.Content) == 0 {
		return nil, fmt.Errorf("failed to parse glab config at %s: empty document", configPath)
	}

	topMap := root.Content[0]
	if topMap.Kind != yaml.MappingNode {
		return nil, fmt.Errorf("failed to parse glab config at %s: expected mapping", configPath)
	}

	// Find the "hosts" key in the top-level mapping.
	var hostsNode *yaml.Node

	for i := 0; i < len(topMap.Content)-1; i += 2 {
		if topMap.Content[i].Value == "hosts" {
			hostsNode = topMap.Content[i+1]

			break
		}
	}

	if hostsNode == nil || hostsNode.Kind != yaml.MappingNode {
		return nil, nil
	}

	var entries []hostEntry

	for i := 0; i < len(hostsNode.Content)-1; i += 2 {
		hostname := hostsNode.Content[i].Value
		valueNode := hostsNode.Content[i+1]

		var cfg glabHostConfig
		if err := valueNode.Decode(&cfg); err != nil {
			return nil, fmt.Errorf("failed to parse host %s in %s: %w", hostname, configPath, err)
		}

		entries = append(entries, hostEntry{host: hostname, config: cfg})
	}

	return entries, nil
}

// DefaultConfigDir returns the default glab config directory path.
func DefaultConfigDir() string {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(os.Getenv("HOME"), ".config", "glab-cli")
	}

	return filepath.Join(configDir, "glab-cli")
}

// protocolOrDefault returns the protocol, defaulting to "https" if empty.
func protocolOrDefault(protocol string) string {
	if protocol == "" {
		return "https"
	}

	return protocol
}
