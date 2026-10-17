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

//nolint:govet,testifylint,noinlineerr // test file - shadows and assertion style acceptable
package dbt

import (
	"fmt"
	"github.com/aws/aws-sdk-go/aws"
	"github.com/aws/aws-sdk-go/service/s3"
	"github.com/stretchr/testify/assert"
	"log"
	"net/http"
	"os"
	"runtime"
	"strings"
	"testing"
)

func TestS3List(t *testing.T) {
	svc := s3.New(s3Session)
	input := &s3.ListObjectsInput{
		Bucket:  aws.String("dbt-tools"),
		MaxKeys: aws.Int64(100),
	}

	_, err := svc.ListObjects(input)
	if err != nil {
		t.Errorf("Error listing s3 objects")
	}
}

func TestRepoGet(t *testing.T) {
	for _, f := range testFilesB {
		t.Run(f.Name, func(t *testing.T) {
			url := fmt.Sprintf("%s%s", testHost, f.URLPath)

			resp, err := http.Get(url)
			if err != nil {
				t.Errorf("Failed to fetch %s: %s", f.Name, err)
			}

			assert.True(t, resp.StatusCode < 300, "Non success error code fetching %s (%d)", url, resp.StatusCode)

			// fetch via s3
			key := strings.TrimPrefix(f.URLPath, fmt.Sprintf("/%s/", f.Repo))
			log.Printf("Fetching %s from s3", key)
			headOptions := &s3.HeadObjectInput{
				Bucket: aws.String(f.Repo),
				Key:    aws.String(key),
			}

			headSvc := s3.New(s3Session)

			_, err = headSvc.HeadObject(headOptions)
			if err != nil {
				t.Errorf("failed to get metadata for %s: %s", f.Name, err)
			}
		})
	}
}

func TestGenerateDbtDir(t *testing.T) {
	var inputs = []struct {
		name string
		path string
	}{
		{
			"reposerver",
			homeDirRepoServer,
		},
		{
			"s3",
			homeDirS3,
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			dbtDirPath := fmt.Sprintf("%s/%s", tc.path, DbtDir)

			if _, err := os.Stat(dbtDirPath); os.IsNotExist(err) {
				t.Errorf("dbt dir %s did not create as expected", dbtDirPath)
			}

			trustPath := fmt.Sprintf("%s/%s", tc.path, TrustDir)

			if _, err := os.Stat(trustPath); os.IsNotExist(err) {
				t.Errorf("trust dir %s did not create as expected", trustPath)
			}

			toolPath := fmt.Sprintf("%s/%s", tc.path, ToolDir)
			if _, err := os.Stat(toolPath); os.IsNotExist(err) {
				t.Errorf("tool dir %s did not create as expected", toolPath)
			}

			configPath := fmt.Sprintf("%s/%s", tc.path, ConfigDir)
			if _, err := os.Stat(configPath); os.IsNotExist(err) {
				t.Errorf("config dir %s did not create as expected", configPath)
			}

		})
	}
}

func TestLoadDbtConfig(t *testing.T) {
	var inputs = []struct {
		name     string
		path     string
		contents string
		expected Config
	}{
		{
			"reposerver",
			homeDirRepoServer,
			testDbtConfigContents(port),
			dbtConfig,
		},
		{
			"s3",
			homeDirS3,
			testDbtConfigS3Contents(),
			s3DbtConfig,
		},
	}

	for _, tc := range inputs {

		t.Run(tc.name, func(t *testing.T) {
			expected := tc.expected
			actual, err := LoadDbtConfig(tc.path, true)
			if err != nil {
				t.Errorf("Error loading config file: %s", err)
			}

			assert.Equal(t, expected, actual, "Parsed config meets expectations")
		})
	}
}

func TestFetchTrustStore(t *testing.T) {
	inputs := []struct {
		name    string
		obj     *DBT
		homedir string
	}{
		{
			"reposerver",

			&DBT{
				Config:  dbtConfig,
				Verbose: true,
			},
			homeDirRepoServer,
		},
		{
			"s3",
			&DBT{
				Config:    s3DbtConfig,
				Verbose:   true,
				S3Session: s3Session,
			},
			homeDirS3,
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.obj.FetchTrustStore(tc.homedir)
			if err != nil {
				t.Errorf("Error fetching trust store: %s", err)
			}

			expected := trustfileContents
			trustPath := fmt.Sprintf("%s/%s", tc.homedir, TruststorePath)

			if _, err := os.Stat(trustPath); os.IsNotExist(err) {
				t.Errorf("File not written")
			}

			actualBytes, err := os.ReadFile(trustPath)
			if err != nil {
				t.Errorf("Error reading trust store: %s", err)
			}

			actual := string(actualBytes)

			assert.Equal(t, expected, actual, "Read truststore contents matches expectations.")

		})
	}
}

func TestDbtIsCurrent(t *testing.T) {
	inputs := []struct {
		name    string
		obj     *DBT
		homedir string
		oldURL  string
		newURL  string
	}{
		{
			"reposerver",

			&DBT{
				Config:  dbtConfig,
				Verbose: true,
			},
			homeDirRepoServer,
			fmt.Sprintf("http://127.0.0.1:%d/dbt/%s/%s/amd64/dbt", port, oldVersion, runtime.GOOS),
			fmt.Sprintf("http://127.0.0.1:%d/dbt/%s/%s/amd64/dbt", port, latestVersion, runtime.GOOS),
		},
		{
			"s3",
			&DBT{
				Config:    s3DbtConfig,
				Verbose:   true,
				S3Session: s3Session,
			},
			homeDirS3,
			fmt.Sprintf("https://dbt.s3.us-east-1.amazonaws.com/%s/%s/amd64/dbt", oldVersion, runtime.GOOS),
			fmt.Sprintf("https://dbt.s3.us-east-1.amazonaws.com/%s/%s/amd64/dbt", latestVersion, runtime.GOOS),
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			targetDir := fmt.Sprintf("%s/%s", tc.homedir, ToolDir)
			fileURL := tc.oldURL
			fileName := fmt.Sprintf("%s/dbt", targetDir)

			err := tc.obj.FetchFile(fileURL, fileName)
			if err != nil {
				t.Errorf("Error fetching file %q: %s\n", fileURL, err)
			}

			ok, err := tc.obj.IsCurrent(fileName)
			if err != nil {
				t.Errorf("error checking to see if download file is current: %s\n", err)
			}

			assert.False(t, ok, "Old version should not show up as current.")

			fileURL = tc.newURL
			fileName = fmt.Sprintf("%s/dbt", targetDir)

			err = tc.obj.FetchFile(fileURL, fileName)
			if err != nil {
				t.Errorf("Error fetching file %q: %s\n", fileURL, err)
			}

			ok, err = tc.obj.IsCurrent(fileName)
			if err != nil {
				t.Errorf("error checking to see if download file is current: %s\n", err)
			}

			assert.True(t, ok, "Current version shows current.")
		})
	}
}

func TestDbtUpgradeInPlace(t *testing.T) {
	inputs := []struct {
		name    string
		obj     *DBT
		homedir string
		oldURL  string
		newURL  string
	}{
		{
			"reposerver",

			&DBT{
				Config:  dbtConfig,
				Verbose: true,
			},
			homeDirRepoServer,
			fmt.Sprintf("http://127.0.0.1:%d/dbt/%s/%s/amd64/dbt", port, oldVersion, runtime.GOOS),
			fmt.Sprintf("http://127.0.0.1:%d/dbt/%s/%s/amd64/dbt", port, latestVersion, runtime.GOOS),
		},
		{
			"s3",
			&DBT{
				Config:    s3DbtConfig,
				Verbose:   true,
				S3Session: s3Session,
			},
			homeDirS3,
			fmt.Sprintf("https://dbt.s3.us-east-1.amazonaws.com/%s/%s/amd64/dbt", oldVersion, runtime.GOOS),
			fmt.Sprintf("https://dbt.s3.us-east-1.amazonaws.com/%s/%s/amd64/dbt", latestVersion, runtime.GOOS),
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			targetDir := fmt.Sprintf("%s/%s", tc.homedir, ToolDir)
			fileURL := tc.oldURL
			fileName := fmt.Sprintf("%s/dbt", targetDir)

			err := tc.obj.FetchFile(fileURL, fileName)
			if err != nil {
				t.Errorf("Error fetching file %q: %s\n", fileURL, err)
			}

			ok, err := tc.obj.IsCurrent(fileName)
			if err != nil {
				t.Errorf("error checking to see if download file is current: %s\n", err)
			}

			assert.False(t, ok, "Old version should not show up as current.")

			err = tc.obj.UpgradeInPlace(tc.homedir, fileName)
			if err != nil {
				t.Errorf("Error upgrading in place: %s", err)
			}

			ok, err = tc.obj.IsCurrent(fileName)
			if err != nil {
				t.Errorf("error checking to see if download file is current: %s\n", err)
			}

			assert.True(t, ok, "Current version shows current.")
		})
	}
}

func TestNewDbt(t *testing.T) {
	var inputs = []struct {
		name    string
		homedir string
	}{
		{
			"reposerver",
			homeDirRepoServer,
		},
		{
			"s3",
			homeDirS3,
		},
	}

	for _, tc := range inputs {
		t.Run(tc.name, func(t *testing.T) {
			configPath := fmt.Sprintf("%s/%s", tc.homedir, ConfigDir)
			fileName := fmt.Sprintf("%s/dbt.json", configPath)

			if _, err := os.Stat(fileName); os.IsNotExist(err) {
				fmt.Printf("Writing test dbt config to %s", fileName)
				err = GenerateDbtDir(tc.homedir, true)
				if err != nil {
					t.Errorf("Error generating dbt dir: %s", err)
				}

				err = os.WriteFile(fileName, []byte(testDbtConfigContents(port)), 0644)
				if err != nil {
					t.Errorf("Error writing config file to %s: %s", fileName, err)
				}
			}

			_, err := NewDbt(tc.homedir)
			if err != nil {
				t.Errorf("Error creating DBT object: %s", err)
			}
		})
	}
}

func TestGetHomeDir(t *testing.T) {
	_, err := GetHomeDir()
	if err != nil {
		t.Errorf("Error getting homedir: %s", err)
	}
}

func TestMultiServerConfigSelectServerViaCLIFlag(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {Repository: "https://prod.example.com"},
			"dev":  {Repository: "https://dev.example.com"},
		},
		DefaultServer: "prod",
	}

	server, name, err := config.SelectServer("dev")
	assert.NoError(t, err)
	assert.Equal(t, "dev", name)
	assert.Equal(t, "https://dev.example.com", server.Repository)
}

func TestMultiServerConfigSelectServerViaEnvVar(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod":    {Repository: "https://prod.example.com"},
			"staging": {Repository: "https://staging.example.com"},
		},
		DefaultServer: "prod",
	}

	t.Setenv(DbtServerEnv, "staging")

	server, name, err := config.SelectServer("")
	assert.NoError(t, err)
	assert.Equal(t, "staging", name)
	assert.Equal(t, "https://staging.example.com", server.Repository)
}

func TestMultiServerConfigSelectServerCLIOverridesEnv(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {Repository: "https://prod.example.com"},
			"dev":  {Repository: "https://dev.example.com"},
		},
	}

	t.Setenv(DbtServerEnv, "dev")

	server, name, err := config.SelectServer("prod")
	assert.NoError(t, err)
	assert.Equal(t, "prod", name)
	assert.Equal(t, "https://prod.example.com", server.Repository)
}

func TestMultiServerConfigSelectServerUsesDefault(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {Repository: "https://prod.example.com"},
			"dev":  {Repository: "https://dev.example.com"},
		},
		DefaultServer: "prod",
	}

	server, name, err := config.SelectServer("")
	assert.NoError(t, err)
	assert.Equal(t, "prod", name)
	assert.Equal(t, "https://prod.example.com", server.Repository)
}

func TestMultiServerConfigSelectServerFallsBackToLegacy(t *testing.T) {
	config := MultiServerConfig{
		Dbt: &DbtConfig{
			Repo:       "https://legacy.example.com",
			TrustStore: "https://legacy.example.com/truststore",
		},
		Tools: &ToolsConfig{
			Repo: "https://legacy.example.com/tools",
		},
	}

	server, name, err := config.SelectServer("")
	assert.NoError(t, err)
	assert.Equal(t, "default", name)
	assert.Equal(t, "https://legacy.example.com", server.Repository)
}

func TestMultiServerConfigSelectServerErrorUnknownCLI(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {Repository: "https://prod.example.com"},
		},
	}

	_, _, err := config.SelectServer("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server \"nonexistent\" not found")
}

func TestMultiServerConfigSelectServerErrorUnknownEnv(t *testing.T) {
	config := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {Repository: "https://prod.example.com"},
		},
	}

	t.Setenv(DbtServerEnv, "nonexistent")

	_, _, err := config.SelectServer("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "server \"nonexistent\"")
}

func TestMultiServerConfigSelectServerErrorNoServers(t *testing.T) {
	config := MultiServerConfig{}

	_, _, err := config.SelectServer("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no servers configured")
}

func TestMultiServerConfigToConfig(t *testing.T) {
	multiConfig := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"prod": {
				Repository:      "https://prod.example.com",
				Truststore:      "https://prod.example.com/truststore",
				ToolsRepository: "https://prod.example.com/tools",
				AuthType:        "oidc",
				IssuerURL:       "https://issuer.example.com",
				OIDCAudience:    "dbt-server",
				ConnectorID:     "ssh",
			},
		},
		Username: "testuser",
		Password: "testpass",
	}

	serverConfig := multiConfig.Servers["prod"]
	config := multiConfig.ToConfig(serverConfig)

	assert.Equal(t, "https://prod.example.com", config.Dbt.Repo)
	assert.Equal(t, "https://prod.example.com/truststore", config.Dbt.TrustStore)
	assert.Equal(t, "https://prod.example.com/tools", config.Tools.Repo)
	assert.Equal(t, "oidc", config.AuthType)
	assert.Equal(t, "https://issuer.example.com", config.IssuerURL)
	assert.Equal(t, "dbt-server", config.OIDCAudience)
	assert.Equal(t, "ssh", config.ConnectorID)
	assert.Equal(t, "testuser", config.Username)
	assert.Equal(t, "testpass", config.Password)
}

func TestMultiServerConfigToLegacyServer(t *testing.T) {
	multiConfig := MultiServerConfig{
		Dbt: &DbtConfig{
			Repo:       "https://legacy.example.com",
			TrustStore: "https://legacy.example.com/truststore",
		},
		Tools: &ToolsConfig{
			Repo: "https://legacy.example.com/tools",
		},
		AuthType:     "oidc",
		IssuerURL:    "https://issuer.example.com",
		OIDCAudience: "legacy-audience",
		ConnectorID:  "ldap",
	}

	server := multiConfig.toLegacyServer()

	assert.Equal(t, "https://legacy.example.com", server.Repository)
	assert.Equal(t, "https://legacy.example.com/truststore", server.Truststore)
	assert.Equal(t, "https://legacy.example.com/tools", server.ToolsRepository)
	assert.Equal(t, "oidc", server.AuthType)
	assert.Equal(t, "https://issuer.example.com", server.IssuerURL)
	assert.Equal(t, "legacy-audience", server.OIDCAudience)
	assert.Equal(t, "ldap", server.ConnectorID)
}

func TestMultiServerConfigIsMultiServer(t *testing.T) {
	testCases := []struct {
		name     string
		config   MultiServerConfig
		expected bool
	}{
		{
			name: "multi-server config",
			config: MultiServerConfig{
				Servers: map[string]ServerConfig{
					"prod": {Repository: "https://prod.example.com"},
				},
			},
			expected: true,
		},
		{
			name: "legacy config",
			config: MultiServerConfig{
				Dbt: &DbtConfig{
					Repo: "https://legacy.example.com",
				},
			},
			expected: false,
		},
		{
			name:     "empty config",
			config:   MultiServerConfig{},
			expected: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := tc.config.IsMultiServer()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestLoadMultiServerConfigMultiFormat(t *testing.T) {
	tempDir := t.TempDir()

	// Create the config directory structure
	configDir := fmt.Sprintf("%s/%s", tempDir, ConfigDir)
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config dir: %s", err)
	}

	configContent := `{
		"servers": {
			"prod": {
				"repository": "https://prod.example.com",
				"truststore": "https://prod.example.com/truststore",
				"toolsRepository": "https://prod.example.com/tools",
				"authType": "oidc",
				"issuerUrl": "https://issuer.example.com"
			},
			"dev": {
				"repository": "https://dev.example.com"
			}
		},
		"defaultServer": "prod"
	}`

	configFile := fmt.Sprintf("%s/dbt.json", configDir)
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %s", err)
	}

	config, err := LoadMultiServerConfig(tempDir, false)
	assert.NoError(t, err)
	assert.Equal(t, 2, len(config.Servers))
	assert.Equal(t, "prod", config.DefaultServer)
	assert.Equal(t, "https://prod.example.com", config.Servers["prod"].Repository)
	assert.Equal(t, "oidc", config.Servers["prod"].AuthType)
	assert.Equal(t, "https://dev.example.com", config.Servers["dev"].Repository)
}

func TestLoadMultiServerConfigLegacyFormat(t *testing.T) {
	tempDir := t.TempDir()

	// Create the config directory structure
	configDir := fmt.Sprintf("%s/%s", tempDir, ConfigDir)
	err := os.MkdirAll(configDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create config dir: %s", err)
	}

	configContent := `{
		"dbt": {
			"repository": "https://legacy.example.com",
			"truststore": "https://legacy.example.com/truststore"
		},
		"tools": {
			"repository": "https://legacy.example.com/tools"
		}
	}`

	configFile := fmt.Sprintf("%s/dbt.json", configDir)
	err = os.WriteFile(configFile, []byte(configContent), 0644)
	if err != nil {
		t.Fatalf("Failed to write config file: %s", err)
	}

	config, err := LoadMultiServerConfig(tempDir, false)
	assert.NoError(t, err)
	assert.Equal(t, 0, len(config.Servers))
	assert.NotNil(t, config.Dbt)
	assert.Equal(t, "https://legacy.example.com", config.Dbt.Repo)
	assert.NotNil(t, config.Tools)
	assert.Equal(t, "https://legacy.example.com/tools", config.Tools.Repo)
}

func TestServerConfigAuthFieldsFallback(t *testing.T) {
	// Test that server-specific auth fields override top-level auth fields
	multiConfig := MultiServerConfig{
		Servers: map[string]ServerConfig{
			"with-auth": {
				Repository:   "https://with-auth.example.com",
				AuthType:     "oidc",
				IssuerURL:    "https://server-specific-issuer.example.com",
				OIDCAudience: "server-audience",
			},
			"without-auth": {
				Repository: "https://without-auth.example.com",
			},
		},
		// Top-level auth settings (legacy)
		AuthType:     "basic",
		IssuerURL:    "https://top-level-issuer.example.com",
		OIDCAudience: "top-level-audience",
	}

	// Test server with its own auth settings
	configWithAuth := multiConfig.ToConfig(multiConfig.Servers["with-auth"])
	assert.Equal(t, "oidc", configWithAuth.AuthType, "Should use server-specific auth type")
	assert.Equal(t, "https://server-specific-issuer.example.com", configWithAuth.IssuerURL, "Should use server-specific issuer")
	assert.Equal(t, "server-audience", configWithAuth.OIDCAudience, "Should use server-specific audience")

	// Test server without auth settings (should fall back to top-level)
	configWithoutAuth := multiConfig.ToConfig(multiConfig.Servers["without-auth"])
	assert.Equal(t, "basic", configWithoutAuth.AuthType, "Should fall back to top-level auth type")
	assert.Equal(t, "https://top-level-issuer.example.com", configWithoutAuth.IssuerURL, "Should fall back to top-level issuer")
	assert.Equal(t, "top-level-audience", configWithoutAuth.OIDCAudience, "Should fall back to top-level audience")
}

func TestToolDirForServer(t *testing.T) {
	testCases := []struct {
		name       string
		serverName string
		expected   string
	}{
		{
			name:       "empty server name returns default",
			serverName: "",
			expected:   ToolDir,
		},
		{
			name:       "default server name returns default",
			serverName: "default",
			expected:   ToolDir,
		},
		{
			name:       "named server returns server-specific path",
			serverName: "prod",
			expected:   ToolDir + "/prod",
		},
		{
			name:       "another named server",
			serverName: "staging",
			expected:   ToolDir + "/staging",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ToolDirForServer(tc.serverName)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDBTGetToolDir(t *testing.T) {
	testCases := []struct {
		name       string
		serverName string
		expected   string
	}{
		{
			name:       "empty server name",
			serverName: "",
			expected:   ToolDir,
		},
		{
			name:       "default server",
			serverName: "default",
			expected:   ToolDir,
		},
		{
			name:       "named server",
			serverName: "prod",
			expected:   ToolDir + "/prod",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dbtObj := &DBT{
				ServerName: tc.serverName,
			}
			result := dbtObj.GetToolDir()
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestDBTEnsureToolDir(t *testing.T) {
	tempDir := t.TempDir()

	// Create the base dbt directory structure
	dbtPath := fmt.Sprintf("%s/%s", tempDir, DbtDir)
	err := os.MkdirAll(dbtPath, 0755)
	if err != nil {
		t.Fatalf("Failed to create dbt dir: %s", err)
	}

	testCases := []struct {
		name       string
		serverName string
		expected   string
	}{
		{
			name:       "default server creates standard tool dir",
			serverName: "",
			expected:   fmt.Sprintf("%s/%s", tempDir, ToolDir),
		},
		{
			name:       "named server creates server-specific dir",
			serverName: "prod",
			expected:   fmt.Sprintf("%s/%s/prod", tempDir, ToolDir),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			dbtObj := &DBT{
				ServerName: tc.serverName,
			}
			err := dbtObj.EnsureToolDir(tempDir)
			assert.NoError(t, err)

			// Verify directory exists
			_, statErr := os.Stat(tc.expected)
			assert.False(t, os.IsNotExist(statErr), "Directory should exist: %s", tc.expected)
		})
	}
}
