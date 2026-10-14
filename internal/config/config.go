// Package config loads validated YAML configuration. Secrets are supplied by environment.
package config

import (
	"fmt"
	"gopkg.in/yaml.v3"
	"os"
	"strings"
	"time"
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
		Type    string        `yaml:"type"`
		Command []string      `yaml:"command"`
		Timeout time.Duration `yaml:"timeout"`
	} `yaml:"claimer"`
}

func Load(path string) (*Config, error) {
	c := &Config{Listen: "127.0.0.1:8080", Database: "data/epic-claim.db", Locale: "en-US", Country: "US"}
	c.Schedule.CheckInterval = time.Hour
	c.Schedule.AccountDelay = 5 * time.Second
	c.Claimer.Type = "exec"
	c.Claimer.Command = []string{"node", "workers/epic/worker.mjs"}
	c.Claimer.Timeout = 5 * time.Minute
	b, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	if err == nil {
		d := yaml.NewDecoder(strings.NewReader(string(b)))
		d.KnownFields(true)
		if err = d.Decode(c); err != nil {
			return nil, err
		}
	}
	if c.Listen == "" || c.Database == "" || c.Locale == "" || len(c.Country) != 2 {
		return nil, fmt.Errorf("listen, database, locale and two-letter country are required")
	}
	if c.Locale != "en-US" {
		return nil, fmt.Errorf("current browser adapter requires locale en-US for title and checkout matching")
	}
	if c.Schedule.AccountDelay < 0 || c.Schedule.AccountDelay > time.Hour || c.Schedule.CheckInterval < time.Minute || c.Schedule.CheckInterval > 7*24*time.Hour {
		return nil, fmt.Errorf("invalid scheduling duration")
	}
	if c.Claimer.Type != "exec" || len(c.Claimer.Command) == 0 || c.Claimer.Timeout < time.Second || c.Claimer.Timeout > 30*time.Minute {
		return nil, fmt.Errorf("configure an exec worker with timeout between 1s and 30m")
	}
	return c, nil
}
