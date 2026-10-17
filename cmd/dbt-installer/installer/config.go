// Copyright 2025 Nik Ogura <nik.ogura@gmail.com>
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package installer

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// Config holds the installer configuration, typically injected at build time.
type Config struct {
	ServerURL         string
	ServerName        string
	ToolsURL          string
	S3Region          string
	IssuerURL         string
	OIDCAudience      string
	OIDCClientID      string
	OIDCClientSecret  string
	ConnectorID       string
	AttestationPolicy string
	Version           string
}

// ServerConfig represents a server entry in the dbt config file.
type ServerConfig struct {
	Repository        string `json:"repository"`
	Truststore        string `json:"truststore,omitempty"`
	ToolsRepository   string `json:"toolsRepository,omitempty"`
	AuthType          string `json:"authType,omitempty"`
	IssuerURL         string `json:"issuerUrl,omitempty"`
	OIDCAudience      string `json:"oidcAudience,omitempty"`
	OIDCClientID      string `json:"oidcClientId,omitempty"`
	OIDCClientSecret  string `json:"oidcClientSecret,omitempty"`
	OIDCUsername      string `json:"oidcUsername,omitempty"`
	ConnectorID       string `json:"connectorId,omitempty"`
	AttestationPolicy string `json:"attestationPolicy,omitempty"`
}

// DbtConfig represents the dbt configuration file structure.
type DbtConfig struct {
	Servers       map[string]ServerConfig `json:"servers"`
	DefaultServer string                  `json:"defaultServer"`
}

// Validate checks that the configuration has all required fields.
func (c *Config) Validate() (err error) {
	if c.ServerURL == "" {
		err = errors.New("server URL is required (must be set at build time via -ldflags)")
		return err
	}

	return err
}

// DeriveDefaults fills in default values for optional configuration fields.
func (c *Config) DeriveDefaults() {
	// Normalize URL
	c.ServerURL = strings.TrimSuffix(c.ServerURL, "/")

	// Derive server name from URL if not provided
	if c.ServerName == "" {
		c.ServerName = deriveServerName(c.ServerURL)
	}

	// Derive tools URL if not provided
	if c.ToolsURL == "" {
		c.ToolsURL = c.ServerURL + "/" + BrandToolsPath
	}

	// Default audience to server URL
	if c.OIDCAudience == "" && c.IssuerURL != "" {
		c.OIDCAudience = c.ServerURL
	}

	// Default client ID based on auth type
	if c.OIDCClientID == "" && c.IssuerURL != "" {
		if c.ConnectorID == connectorSSH {
			c.OIDCClientID = BrandOIDCSSHClientID
		} else {
			c.OIDCClientID = BrandOIDCClientID
		}
	}
}

// deriveServerName extracts a server name from a URL.
const defaultServerName = "default"

func deriveServerName(serverURL string) (name string) {
	name = defaultServerName

	parsed, parseErr := url.Parse(serverURL)
	if parseErr != nil {
		return name
	}

	hostname := parsed.Hostname()
	parts := strings.Split(hostname, ".")

	if len(parts) == 0 {
		return name
	}

	first := parts[0]
	if first == BrandBinary && len(parts) > 1 {
		second := parts[1]
		if second != "example" && second != "com" && second != "org" {
			name = second
			return name
		}
	}

	if first != "" && first != "www" {
		name = first
	}

	return name
}

// BuildServerConfig creates a ServerConfig from the installer Config.
func (c *Config) BuildServerConfig(username string) (server ServerConfig) {
	server = ServerConfig{
		Repository:      c.ServerURL + "/" + BrandBinary,
		Truststore:      c.ServerURL + "/" + BrandBinary + "/truststore",
		ToolsRepository: c.ToolsURL,
	}

	if c.AttestationPolicy != "" {
		server.AttestationPolicy = c.AttestationPolicy
	}

	if c.IssuerURL != "" {
		server.AuthType = "oidc"
		server.IssuerURL = c.IssuerURL
		server.OIDCAudience = c.OIDCAudience
		server.OIDCClientID = c.OIDCClientID
		if c.OIDCClientSecret != "" {
			server.OIDCClientSecret = c.OIDCClientSecret
		}
		if username != "" {
			server.OIDCUsername = username
		}
		if c.ConnectorID != "" {
			server.ConnectorID = c.ConnectorID
		}
	}

	return server
}

// GetConfigPath returns the path to the dbt configuration file.
func GetConfigPath() (configPath string, err error) {
	homeDir, homeErr := os.UserHomeDir()
	if homeErr != nil {
		err = fmt.Errorf("failed to get home directory: %w", homeErr)
		return configPath, err
	}

	configPath = filepath.Join(homeDir, BrandDir, "conf", BrandConfigFile)
	return configPath, err
}

// LoadExistingConfig loads an existing dbt configuration file if it exists.
func LoadExistingConfig(configPath string) (config *DbtConfig, exists bool, err error) {
	data, readErr := os.ReadFile(configPath)
	if os.IsNotExist(readErr) {
		exists = false
		return config, exists, err
	}
	if readErr != nil {
		err = fmt.Errorf("failed to read config file: %w", readErr)
		return config, exists, err
	}

	exists = true
	config = &DbtConfig{}
	unmarshalErr := json.Unmarshal(data, config)
	if unmarshalErr != nil {
		err = fmt.Errorf("failed to parse config file: %w", unmarshalErr)
		return config, exists, err
	}

	// Initialize servers map if nil (legacy config format)
	if config.Servers == nil {
		config.Servers = make(map[string]ServerConfig)
	}

	return config, exists, err
}

// SaveConfig writes the dbt configuration to disk.
func SaveConfig(configPath string, config *DbtConfig) (err error) {
	// Ensure directory exists
	configDir := filepath.Dir(configPath)
	mkdirErr := os.MkdirAll(configDir, 0755)
	if mkdirErr != nil {
		err = fmt.Errorf("failed to create config directory: %w", mkdirErr)
		return err
	}

	// Marshal with indentation for readability
	data, marshalErr := json.MarshalIndent(config, "", "    ")
	if marshalErr != nil {
		err = fmt.Errorf("failed to marshal config: %w", marshalErr)
		return err
	}

	// Write file
	writeErr := os.WriteFile(configPath, data, 0644)
	if writeErr != nil {
		err = fmt.Errorf("failed to write config file: %w", writeErr)
		return err
	}

	return err
}
