package main

import (
	"encoding/json"
	"os"
	"path/filepath"
)

type AppConfig struct {
	DefaultLayout string `json:"default_layout"`
	Theme         string `json:"theme"`
}

var currentConfig AppConfig

func loadConfig() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		currentConfig = AppConfig{DefaultLayout: "default"}
		return
	}
	configPath := filepath.Join(homeDir, ".mactop", "config.json")

	file, err := os.ReadFile(configPath)
	if err != nil {
		currentConfig = AppConfig{DefaultLayout: "default"}
		return
	}

	err = json.Unmarshal(file, &currentConfig)
	if err != nil {
		currentConfig = AppConfig{DefaultLayout: "default"}
	}
}

func saveConfig() {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return
	}
	configDir := filepath.Join(homeDir, ".mactop")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		return
	}
	configPath := filepath.Join(configDir, "config.json")

	data, err := json.MarshalIndent(currentConfig, "", "  ")
	if err != nil {
		return
	}

	os.WriteFile(configPath, data, 0644)
}
