// Package config loads YAML configuration.
package config

import (
	"os"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Listen   string `yaml:"listen"`
	Database string `yaml:"database"`
	Locale   string `yaml:"locale"`
	Country  string `yaml:"country"`
	Schedule struct {
		CheckInterval time.Duration `yaml:"check_interval"`
		AccountDelay  time.Duration `yaml:"account_delay"`
	} `yaml:"schedule"`
	Claimer struct {
		Type         string   `yaml:"type"`
		Command      []string `yaml:"command"`
		HTTPEndpoint string   `yaml:"http_endpoint"`
	} `yaml:"claimer"`
	Notify struct {
		Telegram struct {
			BotToken string `yaml:"bot_token"`
			ChatID   string `yaml:"chat_id"`
		} `yaml:"telegram"`
	} `yaml:"notify"`
}

// Load reads path; a missing file yields defaults.
func Load(path string) (*Config, error) {
	c := &Config{Listen: ":8080", Database: "data/epic-claim.db", Locale: "zh-CN", Country: "CN"}
	c.Schedule.CheckInterval = time.Hour
	c.Schedule.AccountDelay = 2 * time.Minute
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return c, nil
	} else if err != nil {
		return nil, err
	}
	return c, yaml.Unmarshal(b, c)
}
