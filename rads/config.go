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
}

type NatsConfig struct {
	Host string `yaml:"host"`
	Port int    `yaml:"port"`
}

func loadConfig() (*Config, error) {
	path := "./config.yaml"
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
