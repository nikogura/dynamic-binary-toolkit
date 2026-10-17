// Copyright © 2019 Nik Ogura <nik.ogura@gmail.com>
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

package dbt

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"syscall"
	"time"

	"github.com/aws/aws-sdk-go/aws/session"
	"github.com/mitchellh/go-homedir"
	"github.com/pkg/errors"
)

// Legacy constants — kept for backward compatibility.
// New code should use the brand functions: GetBrandDir(), GetTrustDir(), etc.
// These are now computed from brand variables and can be overridden at build time
// via ldflags (see brand.go).

// DbtDir is the standard directory. Usually ~/.dbt. Use GetBrandDir() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetBrandDir() in new code.
var DbtDir = BrandDir

// TrustDir is the trust store directory. Use GetTrustDir() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetTrustDir() in new code.
var TrustDir = BrandDir + "/trust"

// ToolDir is the tool directory. Use GetToolDir() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetToolDir() in new code.
var ToolDir = BrandDir + "/tools"

// ConfigDir is the configuration directory. Use GetConfigDir() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetConfigDir() in new code.
var ConfigDir = BrandDir + "/conf"

// ConfigFilePath is the config file path. Use GetConfigFilePath() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetConfigFilePath() in new code.
var ConfigFilePath = BrandDir + "/conf/" + BrandConfigFile

// TruststorePath is the truststore file path. Use GetTruststorePath() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetTruststorePath() in new code.
var TruststorePath = BrandDir + "/trust/truststore"

// VERSION is DBT's version. Set to "dev" by default, injected at build time via ldflags.
// Build with: go build -ldflags "-X github.com/nikogura/dynamic-binary-toolkit/pkg/dbt.VERSION=X.Y.Z".
//
//nolint:gochecknoglobals // Must be a var to allow ldflags injection at build time.
var VERSION = "dev"

// defaultServerName is the fallback server name when none is configured.
const defaultServerName = "default"

// DBT is the dbt object itself.
type DBT struct {
	Config     Config
	ServerName string // Name of the server being used (for multi-server support)
	Verbose    bool
	Logger     *log.Logger
	S3Session  *session.Session
	OIDCClient *OIDCClient // Client for OIDC token exchange (nil if not using OIDC)
}

// Config is the configuration of the dbt object.
type Config struct {
	Dbt          DbtConfig   `json:"dbt"`
	Tools        ToolsConfig `json:"tools"`
	Username     string      `json:"username,omitempty"`
	Password     string      `json:"password,omitempty"`
	UsernameFunc string      `json:"usernamefunc,omitempty"`
	PasswordFunc string      `json:"passwordfunc,omitempty"`
	Pubkey       string      `json:"pubkey,omitempty"`
	PubkeyPath   string      `json:"pubkeypath,omitempty"`
	PubkeyFunc   string      `json:"pubkeyfunc,omitempty"`
	// OIDC authentication options (RFC 8693 token exchange)
	AuthType         string `json:"authType,omitempty"`         // "oidc" for OIDC auth, empty for legacy SSH-agent auth
	IssuerURL        string `json:"issuerUrl,omitempty"`        // OIDC issuer URL for token exchange
	OIDCAudience     string `json:"oidcAudience,omitempty"`     // Target audience for OIDC tokens (e.g., "dbt-server")
	OIDCClientID     string `json:"oidcClientId,omitempty"`     // OAuth2 client ID for token exchange
	OIDCClientSecret string `json:"oidcClientSecret,omitempty"` // OAuth2 client secret for token exchange
	ConnectorID      string `json:"connectorId,omitempty"`      // Connector ID for providers that support it (e.g., "ssh" for Dex)
	// AttestationPolicy controls the third verification leg: "optional" (default) verifies
	// attestations when present and refuses present-but-broken ones; "required" additionally
	// refuses tools that carry no attestations.
	AttestationPolicy string `json:"attestationPolicy,omitempty"`
}

// DbtConfig is the internal config of dbt.
type DbtConfig struct {
	Repo       string `json:"repository"`
	TrustStore string `json:"truststore"`
}

// ToolsConfig is the config information for the tools to be downloaded and run.
type ToolsConfig struct {
	Repo string `json:"repository"`
}

// ServerConfig holds configuration for a single dbt server.
type ServerConfig struct {
	Repository       string `json:"repository"`
	Truststore       string `json:"truststore,omitempty"`
	ToolsRepository  string `json:"toolsRepository,omitempty"`
	AuthType         string `json:"authType,omitempty"`
	IssuerURL        string `json:"issuerUrl,omitempty"`
	OIDCAudience     string `json:"oidcAudience,omitempty"`
	OIDCClientID     string `json:"oidcClientId,omitempty"`
	OIDCClientSecret string `json:"oidcClientSecret,omitempty"`
	OIDCUsername     string `json:"oidcUsername,omitempty"`
	ConnectorID      string `json:"connectorId,omitempty"`
	// AttestationPolicy is the per-server policy; it takes precedence over the top-level default.
	AttestationPolicy string `json:"attestationPolicy,omitempty"`
}

// MultiServerConfig holds the top-level config with multiple servers.
type MultiServerConfig struct {
	Servers       map[string]ServerConfig `json:"servers,omitempty"`
	DefaultServer string                  `json:"defaultServer,omitempty"`
	// Legacy fields for backward compatibility
	Dbt   *DbtConfig   `json:"dbt,omitempty"`
	Tools *ToolsConfig `json:"tools,omitempty"`
	// Legacy auth fields
	Username         string `json:"username,omitempty"`
	Password         string `json:"password,omitempty"`
	UsernameFunc     string `json:"usernamefunc,omitempty"`
	PasswordFunc     string `json:"passwordfunc,omitempty"`
	Pubkey           string `json:"pubkey,omitempty"`
	PubkeyPath       string `json:"pubkeypath,omitempty"`
	PubkeyFunc       string `json:"pubkeyfunc,omitempty"`
	AuthType         string `json:"authType,omitempty"`
	IssuerURL        string `json:"issuerUrl,omitempty"`
	OIDCAudience     string `json:"oidcAudience,omitempty"`
	OIDCClientID     string `json:"oidcClientId,omitempty"`
	OIDCClientSecret string `json:"oidcClientSecret,omitempty"`
	ConnectorID      string `json:"connectorId,omitempty"`
	// AttestationPolicy is the top-level default; a per-server value takes precedence.
	AttestationPolicy string `json:"attestationPolicy,omitempty"`
}

// DbtServerEnv is the environment variable for selecting a server. Use GetServerEnvVar() instead.
//
//nolint:gochecknoglobals // Backward compatibility — use GetServerEnvVar() in new code.
var DbtServerEnv = BrandEnvPrefix + "_SERVER"

// SelectServer returns the ServerConfig for the specified server name.
// Priority: cliFlag > envVar > configDefault > first server > legacy config.
func (c *MultiServerConfig) SelectServer(cliFlag string) (server ServerConfig, name string, err error) {
	// Check CLI flag first
	if cliFlag != "" {
		srv, ok := c.Servers[cliFlag]
		if ok {
			name = cliFlag
			server = srv
			return server, name, err
		}
		err = fmt.Errorf("server %q not found in config", cliFlag)
		return server, name, err
	}

	// Check environment variable
	envServer := os.Getenv(DbtServerEnv)
	if envServer != "" {
		srv, ok := c.Servers[envServer]
		if ok {
			name = envServer
			server = srv
			return server, name, err
		}
		err = fmt.Errorf("server %q (from %s) not found in config", envServer, DbtServerEnv)
		return server, name, err
	}

	// Check config default
	if c.DefaultServer != "" {
		srv, ok := c.Servers[c.DefaultServer]
		if ok {
			name = c.DefaultServer
			server = srv
			return server, name, err
		}
	}

	// Use first server if available
	for serverName, srv := range c.Servers {
		name = serverName
		server = srv
		return server, name, err
	}

	// Fall back to legacy config
	if c.Dbt != nil {
		name = defaultServerName
		server = c.toLegacyServer()
		return server, name, err
	}

	err = errors.New("no servers configured")
	return server, name, err
}

// toLegacyServer converts legacy config fields to a ServerConfig.
func (c *MultiServerConfig) toLegacyServer() (server ServerConfig) {
	if c.Dbt != nil {
		server.Repository = c.Dbt.Repo
		server.Truststore = c.Dbt.TrustStore
	}
	if c.Tools != nil {
		server.ToolsRepository = c.Tools.Repo
	}
	server.AuthType = c.AuthType
	server.IssuerURL = c.IssuerURL
	server.OIDCAudience = c.OIDCAudience
	server.OIDCClientID = c.OIDCClientID
	server.OIDCClientSecret = c.OIDCClientSecret
	server.ConnectorID = c.ConnectorID
	server.AttestationPolicy = c.AttestationPolicy
	return server
}

// ToConfig converts a selected ServerConfig back to a legacy Config for use with existing code.
func (c *MultiServerConfig) ToConfig(server ServerConfig) (config Config) {
	config.Dbt = DbtConfig{
		Repo:       server.Repository,
		TrustStore: server.Truststore,
	}
	config.Tools = ToolsConfig{
		Repo: server.ToolsRepository,
	}
	// Copy legacy auth fields, prefer per-server username if set
	if server.OIDCUsername != "" {
		config.Username = server.OIDCUsername
	} else {
		config.Username = c.Username
	}
	config.Password = c.Password
	config.UsernameFunc = c.UsernameFunc
	config.PasswordFunc = c.PasswordFunc
	config.Pubkey = c.Pubkey
	config.PubkeyPath = c.PubkeyPath
	config.PubkeyFunc = c.PubkeyFunc
	// Use server-specific auth settings if present, otherwise fall back to top-level
	if server.AuthType != "" {
		config.AuthType = server.AuthType
	} else {
		config.AuthType = c.AuthType
	}
	if server.IssuerURL != "" {
		config.IssuerURL = server.IssuerURL
	} else {
		config.IssuerURL = c.IssuerURL
	}
	if server.OIDCAudience != "" {
		config.OIDCAudience = server.OIDCAudience
	} else {
		config.OIDCAudience = c.OIDCAudience
	}
	if server.OIDCClientID != "" {
		config.OIDCClientID = server.OIDCClientID
	} else {
		config.OIDCClientID = c.OIDCClientID
	}
	if server.OIDCClientSecret != "" {
		config.OIDCClientSecret = server.OIDCClientSecret
	} else {
		config.OIDCClientSecret = c.OIDCClientSecret
	}
	if server.ConnectorID != "" {
		config.ConnectorID = server.ConnectorID
	} else {
		config.ConnectorID = c.ConnectorID
	}
	if server.AttestationPolicy != "" {
		config.AttestationPolicy = server.AttestationPolicy
	} else {
		config.AttestationPolicy = c.AttestationPolicy
	}
	return config
}

// IsMultiServer returns true if the config uses the multi-server format.
func (c *MultiServerConfig) IsMultiServer() (result bool) {
	result = len(c.Servers) > 0
	return result
}

// NewDbt creates a new dbt object.
func NewDbt(homedir string) (dbt *DBT, err error) {
	config, configErr := LoadDbtConfig(homedir, false)
	if configErr != nil {
		err = errors.Wrapf(configErr, "failed to load config file")
	}

	dbt = &DBT{
		Config:  config,
		Verbose: false,
		Logger:  log.New(os.Stderr, "", 0),
	}

	ok, s3meta := S3Url(config.Dbt.Repo)

	if ok {
		if dbt.S3Session == nil {
			s3Session, sessionErr := DefaultSession(&s3meta)
			if sessionErr != nil {
				err = errors.Wrapf(sessionErr, "failed to create s3 session")
				return dbt, err
			}

			dbt.S3Session = s3Session
		}
	}

	// Initialize OIDC client if configured
	if config.AuthType == AUTH_OIDC {
		oidcConfig := &OIDCClientConfig{
			IssuerURL:        config.IssuerURL,
			OIDCAudience:     config.OIDCAudience,
			OIDCClientID:     config.OIDCClientID,
			OIDCClientSecret: config.OIDCClientSecret,
			OIDCUsername:     config.Username,
			ConnectorID:      config.ConnectorID,
		}
		oidcClient, oidcErr := NewOIDCClient(oidcConfig)
		if oidcErr != nil {
			err = errors.Wrapf(oidcErr, "failed to create OIDC client")
			return dbt, err
		}
		dbt.OIDCClient = oidcClient
	}

	return dbt, err
}

// NewDbtWithServer creates a new dbt object with server selection support.
// The serverFlag parameter specifies which server to use (can be empty for default selection).
func NewDbtWithServer(homedir string, serverFlag string) (dbt *DBT, serverName string, err error) {
	var multiConfig MultiServerConfig
	multiConfig, err = LoadMultiServerConfig(homedir, false)
	if err != nil {
		err = errors.Wrapf(err, "failed to load config file")
		return dbt, serverName, err
	}

	var serverConfig ServerConfig
	serverConfig, serverName, err = multiConfig.SelectServer(serverFlag)
	if err != nil {
		err = errors.Wrapf(err, "failed to select server")
		return dbt, serverName, err
	}

	config := multiConfig.ToConfig(serverConfig)

	dbt = &DBT{
		Config:     config,
		ServerName: serverName,
		Verbose:    false,
		Logger:     log.New(os.Stderr, "", 0),
	}

	ok, s3meta := S3Url(config.Dbt.Repo)

	if ok {
		if dbt.S3Session == nil {
			s3Session, sessionErr := DefaultSession(&s3meta)
			if sessionErr != nil {
				err = errors.Wrapf(sessionErr, "failed to create s3 session")
				return dbt, serverName, err
			}

			dbt.S3Session = s3Session
		}
	}

	// Initialize OIDC client if configured
	if config.AuthType == AUTH_OIDC {
		oidcConfig := &OIDCClientConfig{
			IssuerURL:        config.IssuerURL,
			OIDCAudience:     config.OIDCAudience,
			OIDCClientID:     config.OIDCClientID,
			OIDCClientSecret: config.OIDCClientSecret,
			OIDCUsername:     config.Username,
			ConnectorID:      config.ConnectorID,
		}
		oidcClient, oidcErr := NewOIDCClient(oidcConfig)
		if oidcErr != nil {
			err = errors.Wrapf(oidcErr, "failed to create OIDC client")
			return dbt, serverName, err
		}
		dbt.OIDCClient = oidcClient
	}

	return dbt, serverName, err
}

// SetVerbose sets the verbose option on the dbt object.
func (dbt *DBT) SetVerbose(verbose bool) {
	dbt.Verbose = verbose
}

// ToolDirForServer returns the tool directory path for a specific server.
// For legacy/default configs, returns the standard ToolDir.
// For multi-server configs, returns ToolDir/{serverName}.
func ToolDirForServer(serverName string) (toolDir string) {
	if serverName == "" || serverName == defaultServerName {
		toolDir = ToolDir
		return toolDir
	}
	toolDir = fmt.Sprintf("%s/%s", ToolDir, serverName)
	return toolDir
}

// GetToolDir returns the tool directory for this DBT instance.
func (dbt *DBT) GetToolDir() (toolDir string) {
	toolDir = ToolDirForServer(dbt.ServerName)
	return toolDir
}

// EnsureToolDir ensures the tool directory for this DBT instance exists.
func (dbt *DBT) EnsureToolDir(homedir string) (err error) {
	toolDir := dbt.GetToolDir()
	toolPath := fmt.Sprintf("%s/%s", homedir, toolDir)

	_, statErr := os.Stat(toolPath)
	if os.IsNotExist(statErr) {
		err = os.MkdirAll(toolPath, 0755)
		if err != nil {
			err = errors.Wrapf(err, "failed to create tool directory %s", toolPath)
			return err
		}
	}

	return err
}

// LoadDbtConfig loads the dbt config from the expected location on the filesystem.
func LoadDbtConfig(homedir string, verbose bool) (config Config, err error) {
	if homedir == "" {
		var homedirErr error
		homedir, homedirErr = GetHomeDir()
		if homedirErr != nil {
			err = errors.Wrapf(homedirErr, "failed to get homedir")
			return config, err
		}
	}

	logger := log.New(os.Stderr, "", 0)

	if verbose {
		logger.Printf("Looking for dbt config in %s/.dbt", homedir)
	}

	filePath := fmt.Sprintf("%s/%s", homedir, ConfigFilePath)

	if verbose {
		logger.Printf("Loading config from %s", filePath)
	}

	mdBytes, readErr := os.ReadFile(filePath)
	if readErr != nil {
		err = readErr
		return config, err
	}

	err = json.Unmarshal(mdBytes, &config)
	if err != nil {
		return config, err
	}

	// Fail closed on an unrecognized attestation policy: a typo must never silently
	// downgrade the verification chain to optional.
	err = validateAttestationPolicy(config.AttestationPolicy)
	if err != nil {
		return config, err
	}

	return config, err
}

// LoadMultiServerConfig loads the multi-server dbt config from the expected location on the filesystem.
// This function supports both the new multi-server format and the legacy single-server format.
func LoadMultiServerConfig(homedir string, verbose bool) (config MultiServerConfig, err error) {
	if homedir == "" {
		var homedirErr error
		homedir, homedirErr = GetHomeDir()
		if homedirErr != nil {
			err = errors.Wrapf(homedirErr, "failed to get homedir")
			return config, err
		}
	}

	logger := log.New(os.Stderr, "", 0)

	if verbose {
		logger.Printf("Looking for dbt config in %s/.dbt", homedir)
	}

	filePath := fmt.Sprintf("%s/%s", homedir, ConfigFilePath)

	if verbose {
		logger.Printf("Loading config from %s", filePath)
	}

	mdBytes, readErr := os.ReadFile(filePath)
	if readErr != nil {
		err = readErr
		return config, err
	}

	err = json.Unmarshal(mdBytes, &config)
	if err != nil {
		return config, err
	}

	// Fail closed on an unrecognized attestation policy, both top-level and per-server.
	err = validateAttestationPolicy(config.AttestationPolicy)
	if err != nil {
		return config, err
	}

	for name, server := range config.Servers {
		err = validateAttestationPolicy(server.AttestationPolicy)
		if err != nil {
			err = errors.Wrapf(err, "server %q", name)
			return config, err
		}
	}

	return config, err
}

// GenerateDbtDir generates the necessary dbt dirs in the user's homedir if they don't already exist.  If they do exist, it does nothing.
func GenerateDbtDir(homedir string, verbose bool) (err error) {
	if homedir == "" {
		var homedirErr error
		homedir, homedirErr = GetHomeDir()
		if homedirErr != nil {
			err = errors.Wrapf(homedirErr, "failed to get homedir")
			return err
		}
	}

	logger := log.New(os.Stderr, "", 0)

	if verbose {
		logger.Printf("Creating DBT directory in %s.dbt", homedir)
	}

	dbtPath := fmt.Sprintf("%s/%s", homedir, DbtDir)

	_, dbtStatErr := os.Stat(dbtPath)
	if os.IsNotExist(dbtStatErr) {
		mkdirErr := os.Mkdir(dbtPath, 0755)
		if mkdirErr != nil {
			err = errors.Wrapf(mkdirErr, "failed to create directory %s", dbtPath)
			return err
		}
	}

	trustPath := fmt.Sprintf("%s/%s", homedir, TrustDir)

	_, trustStatErr := os.Stat(trustPath)
	if os.IsNotExist(trustStatErr) {
		mkdirErr := os.Mkdir(trustPath, 0755)
		if mkdirErr != nil {
			err = errors.Wrapf(mkdirErr, "failed to create directory %s", trustPath)
			return err
		}
	}

	toolPath := fmt.Sprintf("%s/%s", homedir, ToolDir)
	err = os.Mkdir(toolPath, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to create directory %s", toolPath)
		return err
	}

	configPath := fmt.Sprintf("%s/%s", homedir, ConfigDir)
	err = os.Mkdir(configPath, 0755)
	if err != nil {
		err = errors.Wrapf(err, "failed to create directory %s", configPath)
		return err
	}

	return err
}

// GetHomeDir gets the current user's homedir.
func GetHomeDir() (dir string, err error) {
	dir, err = homedir.Dir()
	return dir, err
}

// FetchTrustStore writes the downloaded trusted signing public keys to disk.
func (dbt *DBT) FetchTrustStore(homedir string) (err error) {
	uri := dbt.Config.Dbt.TrustStore

	dbt.VerboseOutput("Fetching truststore from %q\n", uri)

	isS3, s3Meta := S3Url(uri)

	if isS3 {
		err = dbt.S3FetchTruststore(homedir, s3Meta)
		return err
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	req, reqErr := http.NewRequestWithContext(context.Background(), http.MethodGet, uri, nil)
	if reqErr != nil {
		err = errors.Wrapf(reqErr, "failed to create request for url: %s", uri)
		return err
	}

	err = dbt.AuthHeaders(req)
	if err != nil {
		err = errors.Wrapf(err, "failed adding auth headers")
		return err
	}

	resp, doErr := client.Do(req)
	if doErr != nil {
		err = errors.Wrapf(doErr, "failed to fetch truststore from %s", uri)
		return err
	}
	if resp != nil {
		defer resp.Body.Close()

		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			err = errors.Wrapf(readErr, "failed to read truststore contents")
			return err
		}

		keytext := string(bodyBytes)

		// don't write anything if we have an empty string
		if keytext != "" {
			filePath := fmt.Sprintf("%s/%s", homedir, TruststorePath)
			writeErr := os.WriteFile(filePath, []byte(keytext), 0644)
			if writeErr != nil {
				err = errors.Wrapf(writeErr, "failed to write trust file")
				return err
			}
		}
	}

	return err
}

// IsCurrent returns whether the currently running version is the latest version.
func (dbt *DBT) IsCurrent(binaryPath string) (ok bool, err error) {
	latest, latestErr := dbt.FindLatestVersion("")
	if latestErr != nil {
		err = errors.Wrap(latestErr, "failed to fetch dbt versions")
		return ok, err
	}

	dbt.VerboseOutput("Latest version: %s\n", latest)

	latestDbtVersionURL := fmt.Sprintf("%s/%s/%s/%s/%s", dbt.Config.Dbt.Repo, latest, runtime.GOOS, runtime.GOARCH, BrandBinary)

	dbt.VerboseOutput("Latest version url: %s\n", latestDbtVersionURL)

	var verifyErr error
	ok, verifyErr = dbt.VerifyFileVersion(latestDbtVersionURL, binaryPath)
	if verifyErr != nil {
		err = errors.Wrap(verifyErr, "failed to check latest version")
		return ok, err
	}

	if !ok {
		dbt.VerboseOutput("File at %s does not match latest", binaryPath)
		_, _ = fmt.Fprintf(os.Stderr, "Newer version of %s available: %s\n\n", BrandName, latest)
	}

	return ok, err
}

// UpgradeInPlace upgrades dbt in place.
func (dbt *DBT) UpgradeInPlace(homedir string, binaryPath string) (err error) {
	dbt.VerboseOutput("Attempting upgrade in place")
	tmpDir, mkdirErr := os.MkdirTemp("", BrandBinary)
	if mkdirErr != nil {
		err = errors.Wrap(mkdirErr, "failed to create temp dir")
		return err
	}

	dbt.VerboseOutput("  Temp Dir: %s", tmpDir)

	defer os.RemoveAll(tmpDir)

	newBinaryFile := fmt.Sprintf("%s/%s", tmpDir, BrandBinary)

	dbt.VerboseOutput("  New binary file: %s", newBinaryFile)

	latest, latestErr := dbt.FindLatestVersion("")
	if latestErr != nil {
		err = errors.Wrap(latestErr, fmt.Sprintf("failed to find latest %s version", BrandName))
		return err
	}

	dbt.VerboseOutput("  Latest: %s", latest)

	latestDbtVersionURL := fmt.Sprintf("%s/%s/%s/%s/%s", dbt.Config.Dbt.Repo, latest, runtime.GOOS, runtime.GOARCH, BrandBinary)

	dbt.VerboseOutput("  Fetching from: %s", latestDbtVersionURL)

	fetchErr := dbt.FetchFile(latestDbtVersionURL, newBinaryFile)
	if fetchErr != nil {
		err = errors.Wrap(fetchErr, "failed to fetch new dbt binary")
		return err
	}

	dbt.VerboseOutput("  Verifying %s", newBinaryFile)
	ok, verifyErr := dbt.VerifyFileVersion(latestDbtVersionURL, newBinaryFile)
	if verifyErr != nil {
		err = errors.Wrap(verifyErr, "failed to verify downloaded binary")
		return err
	}

	if ok {
		// Third leg: the incoming binary must satisfy the attestation policy before it
		// replaces this one. Present-but-broken refuses the upgrade; required-absent was
		// already refused at fetch time.
		err = dbt.checkUpgradeAttestations(homedir, latestDbtVersionURL, newBinaryFile)
		if err != nil {
			return err
		}

		dbt.VerboseOutput("  It's good.  Moving it into place.")

		err = moveBinaryIntoPlace(newBinaryFile, binaryPath)
		if err != nil {
			return err
		}
	}

	return err
}

// moveBinaryIntoPlace replaces binaryPath with the contents of newBinaryFile. This is
// slightly more painful than it might otherwise be in order to handle modern linux
// systems where /tmp is tmpfs (can't just rename cross partition). So instead we read
// the file, write the file to a temp file, and then rename.
func moveBinaryIntoPlace(newBinaryFile string, binaryPath string) (err error) {
	newBinaryTempFile := fmt.Sprintf("%s.new", binaryPath)

	b, readErr := os.ReadFile(newBinaryFile)
	if readErr != nil {
		err = errors.Wrapf(readErr, "failed to read new binary file %s", newBinaryFile)
		return err
	}

	writeErr := os.WriteFile(newBinaryTempFile, b, 0755)
	if writeErr != nil {
		err = errors.Wrapf(writeErr, "failed to write new binary temp file %s", newBinaryTempFile)
		return err
	}

	renameErr := os.Rename(newBinaryTempFile, binaryPath)
	if renameErr != nil {
		err = errors.Wrap(renameErr, "failed to move new binary into place")
		return err
	}

	chmodErr := os.Chmod(binaryPath, 0755)
	if chmodErr != nil {
		err = errors.Wrap(chmodErr, "failed to chmod new dbt binary")
		return err
	}

	return err
}

// RunTool runs the dbt tool indicated by the args.
//
//nolint:gocognit,funlen // tool execution requires multiple decision branches for version handling
func (dbt *DBT) RunTool(version string, args []string, homedir string, offline bool) (err error) {
	toolName := args[0]

	if toolName == "--" {
		toolName = args[1]
		args = args[1:]
	}

	toolDir := dbt.GetToolDir()
	localPath := fmt.Sprintf("%s/%s/%s", homedir, toolDir, toolName)

	// Ensure the tool directory exists
	err = dbt.EnsureToolDir(homedir)
	if err != nil {
		err = errors.Wrap(err, "failed to ensure tool directory exists")
		return err
	}

	// if offline, if tool is present and verifies, run it
	if offline {
		err = dbt.verifyAndRun(homedir, args)
		if err != nil {
			err = errors.Wrap(err, "offline run failed")
			return err
		}

		return err
	}

	// we're not offline, so find the latest
	latestVersion, latestErr := dbt.FindLatestVersion(toolName)
	if latestErr != nil {
		err = errors.Wrap(latestErr, "failed to find latest version")
		return err
	}

	// if it's not in the repo, it might still be on the filesystem
	if latestVersion == "" {
		// if it is indeed on the filesystem
		_, localStatErr := os.Stat(localPath)
		if !os.IsNotExist(localStatErr) {
			// attempt to run it in offline mode
			runErr := dbt.verifyAndRun(homedir, args)
			if runErr != nil {
				err = errors.Wrap(runErr, "offline run failed")
				return err
			}

			// and return if it's successful
			return err
		}

		// It's not in the repo, and not on the filesystem, there's not a damn thing we can do.  Fail.
		err = fmt.Errorf("tool %s is not in repo and has not been previously downloaded", toolName)
		return err
	}

	// if version is unset, version is latest version
	if version == "" {
		version = latestVersion
	}

	// url should be http(s)://tool-repo/toolName/version/os/arch/tool
	toolURL := fmt.Sprintf("%s/%s/%s/%s/%s/%s", dbt.Config.Tools.Repo, toolName, version, runtime.GOOS, runtime.GOARCH, toolName)

	_, toolStatErr := os.Stat(localPath)
	if !os.IsNotExist(toolStatErr) {
		// check to see if the latest version is what we have
		uptodate, verifyErr := dbt.VerifyFileVersion(toolURL, localPath)
		if verifyErr != nil {
			err = errors.Wrap(verifyErr, "failed to verify file version")
			return err
		}

		// if yes, run it
		if uptodate {
			runErr := dbt.verifyAndRun(homedir, args)
			if runErr != nil {
				err = errors.Wrap(runErr, "run failed")
				return err
			}

			return err
		}
	}

	// download the binary
	dbt.Logger.Printf("Downloading binary tool %q version %s.", toolName, version)
	err = dbt.FetchFile(toolURL, localPath)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("failed to fetch binary for %s from %s", toolName, toolURL))
		return err
	}

	// download the checksum
	toolChecksumURL := fmt.Sprintf("%s.sha256", toolURL)
	toolChecksumFile := fmt.Sprintf("%s.sha256", localPath)

	err = dbt.FetchFile(toolChecksumURL, toolChecksumFile)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("failed to fetch checksum for %s from %s", toolName, toolChecksumURL))
		return err
	}

	// download the signature
	toolSignatureURL := fmt.Sprintf("%s.asc", toolURL)
	toolSignatureFile := fmt.Sprintf("%s.asc", localPath)

	err = dbt.FetchFile(toolSignatureURL, toolSignatureFile)
	if err != nil {
		err = errors.Wrap(err, fmt.Sprintf("failed to fetch signature for %s from %s", toolName, toolSignatureURL))
		return err
	}

	// download the attestations, if the repository publishes them. Under a
	// required policy, absence is refused here — at download time, the earliest and
	// clearest point — rather than at verify time.
	err = dbt.FetchAttestations(toolURL, localPath, toolName)
	if err != nil {
		return err
	}

	// finally run it
	err = dbt.verifyAndRun(homedir, args)

	return err
}

func (dbt *DBT) verifyAndRun(homedir string, args []string) (err error) {
	toolName := args[0]
	if toolName == "--" {
		toolName = args[1]
		args = args[1:]
	}

	toolDir := dbt.GetToolDir()
	localPath := fmt.Sprintf("%s/%s/%s", homedir, toolDir, toolName)
	localChecksumPath := fmt.Sprintf("%s/%s/%s.sha256", homedir, toolDir, toolName)

	dbt.VerboseOutput("Verifying %q", localPath)

	checksumBytes, readErr := os.ReadFile(localChecksumPath)
	if readErr != nil {
		err = errors.Wrap(readErr, "error reading local checksum file")
		return err
	}

	_, localStatErr := os.Stat(localPath)
	//nolint:nestif // verification flow requires multiple nested checks
	if !os.IsNotExist(localStatErr) {
		checksumOk, checksumErr := dbt.VerifyFileChecksum(localPath, string(checksumBytes))
		if checksumErr != nil {
			err = errors.Wrap(checksumErr, "error validating checksum")
			return err
		}

		if !checksumOk {
			err = fmt.Errorf("checksum of %s failed to verify", toolName)
			return err
		}

		signatureOk, sigErr := dbt.VerifyFileSignature(homedir, localPath)
		if sigErr != nil {
			err = errors.Wrap(sigErr, "error validating signature")
			return err
		}

		if !signatureOk {
			err = fmt.Errorf("signature of %s failed to verify", toolName)
			return err
		}

		// Third leg: attestations. Present-but-broken is always fatal; absence is fatal
		// only under a required policy.
		err = dbt.checkAttestationPolicy(homedir, localPath, toolName)
		if err != nil {
			return err
		}

		execErr := dbt.runExec(homedir, args)
		if execErr != nil {
			err = errors.Wrap(execErr, "failed to run already downloaded tool")
			return err
		}
	}

	return err
}

//nolint:gochecknoglobals // test flag to prevent actual exec calls in tests
var testExec bool

func (dbt *DBT) runExec(homedir string, args []string) (err error) {
	toolName := args[0]
	toolDir := dbt.GetToolDir()
	localPath := fmt.Sprintf("%s/%s/%s", homedir, toolDir, toolName)

	env := os.Environ()

	// Inject repository URLs so tools can use them without re-parsing config.
	if dbt.Config.Dbt.Repo != "" {
		env = append(env, fmt.Sprintf("%s=%s", GetRepoEnvVar(), dbt.Config.Dbt.Repo))
	}
	if dbt.Config.Tools.Repo != "" {
		env = append(env, fmt.Sprintf("%s=%s", GetToolsRepoEnvVar(), dbt.Config.Tools.Repo))
	}
	if dbt.Config.Dbt.TrustStore != "" {
		env = append(env, fmt.Sprintf("%s=%s", GetTruststoreEnvVar(), dbt.Config.Dbt.TrustStore))
	}
	if dbt.ServerName != "" {
		env = append(env, fmt.Sprintf("%s=%s", GetServerEnvVar(), dbt.ServerName))
	}

	if testExec {
		cs := []string{"-test.run=TestHelperProcess", "--", localPath}
		cs = append(cs, args...)
		cmd := exec.CommandContext(context.Background(), os.Args[0], cs...)
		cmdBytes, cmdErr := cmd.Output()
		if cmdErr != nil {
			err = errors.Wrap(cmdErr, "error running exec")
			return err
		}

		fmt.Printf("\nTest Command Output: %q\n", string(cmdBytes))

	} else {
		execErr := syscall.Exec(localPath, args, env)
		if execErr != nil {
			err = errors.Wrap(execErr, "error running exec")
			return err
		}

	}

	return err
}

// VerboseOutput Convenience function so I don't have to write 'if verbose {...}' all the time.
func (dbt *DBT) VerboseOutput(message string, args ...interface{}) {
	if dbt.Verbose {
		if len(args) == 0 {
			fmt.Printf("%s\n", message)
			return
		}

		msg := fmt.Sprintf(message, args...)
		fmt.Printf("%s\n", msg)
	}
}
