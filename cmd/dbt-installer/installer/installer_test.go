package installer

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEnsureDirectories(t *testing.T) {
	// Use a temp dir as HOME so we don't pollute the real home.
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	err := ensureDirectories()
	require.NoError(t, err)

	expectedDirs := []string{
		filepath.Join(tmpHome, ".dbt"),
		filepath.Join(tmpHome, ".dbt", "trust"),
		filepath.Join(tmpHome, ".dbt", "tools"),
		filepath.Join(tmpHome, ".dbt", "conf"),
	}

	for _, dir := range expectedDirs {
		info, statErr := os.Stat(dir)
		require.NoError(t, statErr, "directory %s should exist", dir)
		assert.True(t, info.IsDir(), "%s should be a directory", dir)
	}
}

func TestEnsureDirectoriesIdempotent(t *testing.T) {
	tmpHome := t.TempDir()
	t.Setenv("HOME", tmpHome)

	// Run twice - should not error on existing directories.
	err := ensureDirectories()
	require.NoError(t, err)

	err = ensureDirectories()
	require.NoError(t, err)
}

func TestDeriveServerName(t *testing.T) {
	testCases := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "dbt subdomain extracts second part",
			url:      "https://dbt.corp.terrace.fi",
			expected: "corp",
		},
		{
			name:     "dbt example domain returns default",
			url:      "https://dbt.example.com",
			expected: "dbt",
		},
		{
			name:     "non-dbt hostname returns first part",
			url:      "https://myserver.example.com",
			expected: "myserver",
		},
		{
			name:     "invalid URL returns default",
			url:      "://invalid",
			expected: "default",
		},
		{
			name:     "empty URL returns default",
			url:      "",
			expected: "default",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := deriveServerName(tc.url)
			assert.Equal(t, tc.expected, result)
		})
	}
}

func TestConfigValidate(t *testing.T) {
	t.Run("missing server URL", func(t *testing.T) {
		config := &Config{}
		err := config.Validate()
		assert.Error(t, err)
	})

	t.Run("valid config", func(t *testing.T) {
		config := &Config{ServerURL: "https://dbt.example.com"}
		err := config.Validate()
		assert.NoError(t, err)
	})
}

func TestConfigDeriveDefaults(t *testing.T) {
	config := &Config{
		ServerURL:   "https://dbt.corp.terrace.fi/",
		IssuerURL:   "https://dex.example.com",
		ConnectorID: connectorSSH,
	}

	config.DeriveDefaults()

	assert.Equal(t, "https://dbt.corp.terrace.fi", config.ServerURL, "trailing slash should be trimmed")
	assert.Equal(t, "corp", config.ServerName, "server name should be derived")
	assert.Equal(t, "https://dbt.corp.terrace.fi/dbt-tools", config.ToolsURL, "tools URL should be derived")
	assert.Equal(t, "https://dbt.corp.terrace.fi", config.OIDCAudience, "audience should default to server URL")
	assert.Equal(t, "dbt-ssh", config.OIDCClientID, "client ID should default to dbt-ssh for SSH connector")
}

func TestConfigDeriveDefaultsNoOverwrite(t *testing.T) {
	config := &Config{
		ServerURL:    "https://dbt.corp.terrace.fi",
		ServerName:   "terrace",
		ToolsURL:     "https://tools.example.com",
		OIDCAudience: "custom-audience",
		OIDCClientID: "custom-client",
		IssuerURL:    "https://dex.example.com",
	}

	config.DeriveDefaults()

	assert.Equal(t, "terrace", config.ServerName, "explicit server name should not be overwritten")
	assert.Equal(t, "https://tools.example.com", config.ToolsURL, "explicit tools URL should not be overwritten")
	assert.Equal(t, "custom-audience", config.OIDCAudience, "explicit audience should not be overwritten")
	assert.Equal(t, "custom-client", config.OIDCClientID, "explicit client ID should not be overwritten")
}

func TestIsInPath(t *testing.T) {
	t.Run("directory in PATH", func(t *testing.T) {
		pathEnv := os.Getenv("PATH")
		paths := filepath.SplitList(pathEnv)
		if len(paths) > 0 {
			assert.True(t, isInPath(paths[0]))
		}
	})

	t.Run("directory not in PATH", func(t *testing.T) {
		assert.False(t, isInPath("/nonexistent/path/that/should/not/be/in/PATH"))
	})
}

func TestBuildServerConfigAttestationPolicy(t *testing.T) {
	t.Parallel()

	t.Run("policy set is written to server config", func(t *testing.T) {
		t.Parallel()

		config := &Config{
			ServerURL:         "https://dbt.example.com",
			AttestationPolicy: "required",
		}

		server := config.BuildServerConfig("")

		assert.Equal(t, "required", server.AttestationPolicy, "attestation policy must be written to the server config")
	})

	t.Run("empty policy omits the field", func(t *testing.T) {
		t.Parallel()

		config := &Config{
			ServerURL: "https://dbt.example.com",
		}

		server := config.BuildServerConfig("")

		assert.Empty(t, server.AttestationPolicy, "empty attestation policy must stay omitted (optional default)")
	})
}
