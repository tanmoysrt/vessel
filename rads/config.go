package main

import (
	"fmt"
	"os"

	"github.com/goccy/go-yaml"
)

type Config struct {
	AgentID          string     `yaml:"agent_id"`
	DatabaseFilePath string     `yaml:"database_file_path"`
	NatsConfig       NatsConfig `yaml:"nats_config"`
	ADSConfig        ADSConfig  `yaml:"ads_config"`
}

type NatsConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
	NKey string `yaml:"nkey"`
	JWT  string `yaml:"jwt"`
}

type ADSConfig struct {
	Debug                         bool `yaml:"debug"`
	BindPort                      int  `yaml:"bind_port"`
	NumOfTrustedHops              int  `yaml:"num_of_trusted_hops"`
	EnableDownstreamProxyProtocol bool `yaml:"enable_downstream_proxy_protocol"` // Enable PROXY protocol for all listeners globally
}

func loadConfig() (*Config, error) {
	path := os.Getenv("CONFIG_FILE_PATH")
	if path == "" {
		path = "./config.yaml"
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	var config Config
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return &config, nil
}
