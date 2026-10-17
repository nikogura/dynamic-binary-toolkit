// Copyright © 2026 Nik Ogura <nik.ogura@gmail.com>
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
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/nikogura/dynamic-binary-toolkit/pkg/dbt/testfixtures"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// subjectSha256 is the sha256 digest the valid fixture statements below bind their subject to.
const subjectSha256 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"

// validSLSAStatement is a well-formed in-toto Statement carrying SLSA v1.0 provenance,
// bound to subjectSha256.
const validSLSAStatement = `{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "catalog",
      "digest": {"sha256": "` + subjectSha256 + `"}
    }
  ],
  "predicateType": "https://slsa.dev/provenance/v1",
  "predicate": {
    "buildDefinition": {
      "buildType": "https://gomason.nikogura.com/build/v1",
      "externalParameters": {
        "package": "github.com/nikogura/dynamic-binary-toolkit",
        "version": "3.0.0"
      },
      "resolvedDependencies": []
    },
    "runDetails": {
      "builder": {"id": "tester@nikogura.com"}
    }
  }
}`

// validSBOMStatement is a well-formed in-toto Statement whose predicate is a CycloneDX BOM,
// bound to subjectSha256.
const validSBOMStatement = `{
  "_type": "https://in-toto.io/Statement/v1",
  "subject": [
    {
      "name": "catalog",
      "digest": {"sha256": "` + subjectSha256 + `"}
    }
  ],
  "predicateType": "https://cyclonedx.org/bom",
  "predicate": {
    "bomFormat": "CycloneDX",
    "specVersion": "1.6",
    "components": [
      {
        "type": "library",
        "name": "github.com/spf13/cobra",
        "version": "v1.8.1",
        "purl": "pkg:golang/github.com/spf13/cobra@v1.8.1"
      }
    ]
  }
}`

// writeTempStatement writes contents to a file in t's temp dir and returns the path.
func writeTempStatement(t *testing.T, contents string) (path string) {
	t.Helper()

	path = filepath.Join(t.TempDir(), "statement.json")

	err := os.WriteFile(path, []byte(contents), 0600)
	require.NoError(t, err, "failed writing temp statement fixture")

	return path
}

func TestLoadAttestationStatementValidSLSA(t *testing.T) {
	t.Parallel()

	path := writeTempStatement(t, validSLSAStatement)

	stmt, err := LoadAttestationStatement(path, PredicateTypeSLSAProvenanceV1)
	require.NoError(t, err, "valid SLSA statement must load")

	assert.Equal(t, StatementTypeV1, stmt.Type, "statement _type")
	assert.Equal(t, PredicateTypeSLSAProvenanceV1, stmt.PredicateType, "statement predicateType")
	assert.Equal(t, "catalog", stmt.Subject[0].Name, "subject name")
	assert.Equal(t, subjectSha256, stmt.Subject[0].Digest["sha256"], "subject sha256 digest")
	assert.NotEmpty(t, stmt.Predicate, "predicate bytes must be preserved")
}

func TestLoadAttestationStatementValidCycloneDX(t *testing.T) {
	t.Parallel()

	path := writeTempStatement(t, validSBOMStatement)

	stmt, err := LoadAttestationStatement(path, PredicateTypeCycloneDXBOM)
	require.NoError(t, err, "valid CycloneDX attestation must load")

	assert.Equal(t, PredicateTypeCycloneDXBOM, stmt.PredicateType, "statement predicateType")
	assert.Equal(t, subjectSha256, stmt.Subject[0].Digest["sha256"], "subject sha256 digest")
	assert.NotEmpty(t, stmt.Predicate, "predicate bytes must be preserved")
}

func TestLoadAttestationStatementRejections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contents    string
		expected    string
		expectError bool
	}{
		{
			name:        "malformed json",
			contents:    `{"_type": "https://in-toto.io/Statement/v1",`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name:        "missing file",
			contents:    "",
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "wrong statement type",
			contents: `{
				"_type": "https://in-toto.io/Statement/v0.1",
				"subject": [{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}}],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "missing statement type",
			contents: `{
				"subject": [{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}}],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "predicate type mismatch",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}}],
				"predicateType": "https://spdx.dev/Document",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "empty predicate type",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}}],
				"predicateType": "",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "no subjects",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "multiple subjects",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [
					{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}},
					{"name": "other", "digest": {"sha256": "deadbeef"}}
				],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "empty digest map",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {}}],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
		{
			name: "missing predicate",
			contents: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {"sha256": "` + subjectSha256 + `"}}],
				"predicateType": "https://slsa.dev/provenance/v1"
			}`,
			expected:    PredicateTypeSLSAProvenanceV1,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if tc.name == "missing file" {
				// A path that does not exist exercises the read failure, not the parse.
				_, err := LoadAttestationStatement(filepath.Join(t.TempDir(), "nope.json"), tc.expected)
				assert.Error(t, err, "missing file must fail to load")

				return
			}

			path := writeTempStatement(t, tc.contents)

			_, err := LoadAttestationStatement(path, tc.expected)
			if tc.expectError {
				assert.Error(t, err, "statement must be rejected")
			} else {
				assert.NoError(t, err, "statement must load")
			}
		})
	}
}

func TestVerifySubjectBinding(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		toolName   string
		sha256     string
		expected   string
		statement  string
		shouldPass bool
	}{
		{
			name:       "matching name and digest",
			toolName:   "catalog",
			sha256:     subjectSha256,
			expected:   PredicateTypeSLSAProvenanceV1,
			statement:  validSLSAStatement,
			shouldPass: true,
		},
		{
			name:       "digest mismatch",
			toolName:   "catalog",
			sha256:     "0000000000000000000000000000000000000000000000000000000000000000",
			expected:   PredicateTypeSLSAProvenanceV1,
			statement:  validSLSAStatement,
			shouldPass: false,
		},
		{
			name:       "name mismatch",
			toolName:   "someothertool",
			sha256:     subjectSha256,
			expected:   PredicateTypeSLSAProvenanceV1,
			statement:  validSLSAStatement,
			shouldPass: false,
		},
		{
			name:     "missing sha256 digest key",
			toolName: "catalog",
			sha256:   subjectSha256,
			expected: PredicateTypeSLSAProvenanceV1,
			statement: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {"sha512": "whatever"}}],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			shouldPass: false,
		},
		{
			name:     "empty sha256 digest value",
			toolName: "catalog",
			sha256:   subjectSha256,
			expected: PredicateTypeSLSAProvenanceV1,
			statement: `{
				"_type": "https://in-toto.io/Statement/v1",
				"subject": [{"name": "catalog", "digest": {"sha256": ""}}],
				"predicateType": "https://slsa.dev/provenance/v1",
				"predicate": {}
			}`,
			shouldPass: false,
		},
		{
			name:       "sbom statement with matching binding",
			toolName:   "catalog",
			sha256:     subjectSha256,
			expected:   PredicateTypeCycloneDXBOM,
			statement:  validSBOMStatement,
			shouldPass: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := writeTempStatement(t, tc.statement)

			stmt, err := LoadAttestationStatement(path, tc.expected)
			require.NoError(t, err, "test fixture statement must load")

			ok, bindErr := stmt.VerifySubjectBinding(tc.toolName, tc.sha256)
			require.NoError(t, bindErr, "binding check reports operational errors, not verdicts")

			if tc.shouldPass {
				assert.True(t, ok, "subject binding must pass")
			} else {
				assert.False(t, ok, "subject binding must fail")
			}
		})
	}
}

func TestAttestationStatementJSONRoundTrip(t *testing.T) {
	t.Parallel()

	// The predicate is opaque to dbt, but it must survive a load intact so callers can
	// log or inspect it without re-reading the file.
	path := writeTempStatement(t, validSLSAStatement)

	stmt, err := LoadAttestationStatement(path, PredicateTypeSLSAProvenanceV1)
	require.NoError(t, err, "valid statement must load")

	// Decoded into a narrow local struct: dbt never interprets the predicate in production
	// code; this only proves the preserved bytes are complete and valid.
	predicate := struct {
		RunDetails struct {
			Builder struct {
				ID string `json:"id"`
			} `json:"builder"`
		} `json:"runDetails"`
	}{}

	unmarshalErr := json.Unmarshal(stmt.Predicate, &predicate)
	require.NoError(t, unmarshalErr, "preserved predicate bytes must remain valid JSON")

	assert.Equal(t, "tester@nikogura.com", predicate.RunDetails.Builder.ID, "predicate contents intact")
}

// writeTempDbtConfig writes a dbt.json with the given contents into a temp homedir and
// returns the homedir path, matching the layout LoadDbtConfig and LoadMultiServerConfig
// expect (<homedir>/.dbt/conf/dbt.json).
func writeTempDbtConfig(t *testing.T, contents string) (homedir string) {
	t.Helper()

	homedir = t.TempDir()

	// ConfigFilePath is home-relative (.dbt/conf/dbt.json); the conf dir is its parent.
	confDir := filepath.Join(homedir, filepath.Dir(ConfigFilePath))
	err := os.MkdirAll(confDir, 0755)
	require.NoError(t, err, "failed creating temp config dir")

	err = os.WriteFile(filepath.Join(homedir, ConfigFilePath), []byte(contents), 0600)
	require.NoError(t, err, "failed writing temp dbt config")

	return homedir
}

func TestValidateAttestationPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		policy      string
		expectError bool
	}{
		{
			name:        "empty defaults to optional",
			policy:      "",
			expectError: false,
		},
		{
			name:        "optional",
			policy:      AttestationPolicyOptional,
			expectError: false,
		},
		{
			name:        "required",
			policy:      AttestationPolicyRequired,
			expectError: false,
		},
		{
			name:        "typo requird",
			policy:      "requird",
			expectError: true,
		},
		{
			name:        "uppercase not accepted",
			policy:      "REQUIRED",
			expectError: true,
		},
		{
			name:        "free text not accepted",
			policy:      "if you feel like it",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := validateAttestationPolicy(tc.policy)
			if tc.expectError {
				assert.Error(t, err, "policy must be rejected")
			} else {
				assert.NoError(t, err, "policy must be accepted")
			}
		})
	}
}

func TestLoadDbtConfigAttestationPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		policy      string
		expectError bool
	}{
		{
			name:        "required policy loads",
			policy:      AttestationPolicyRequired,
			expectError: false,
		},
		{
			name:        "empty policy loads as optional",
			policy:      "",
			expectError: false,
		},
		{
			name:        "typo policy refused",
			policy:      "requird",
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			policyEntry := ""
			if tc.policy != "" {
				policyEntry = fmt.Sprintf(`"attestationPolicy": "%s",`, tc.policy)
			}

			contents := fmt.Sprintf(`{
				%s
				"dbt": {
					"repository": "https://dbt.example.com",
					"truststore": "https://dbt.example.com/truststore"
				},
				"tools": {
					"repository": "https://dbt-tools.example.com"
				}
			}`, policyEntry)

			homedir := writeTempDbtConfig(t, contents)

			config, err := LoadDbtConfig(homedir, false)
			if tc.expectError {
				assert.Error(t, err, "config with invalid attestation policy must be refused at load")

				return
			}

			require.NoError(t, err, "valid config must load")
			assert.Equal(t, tc.policy, config.AttestationPolicy, "attestation policy must round-trip")
		})
	}
}

func TestLoadMultiServerConfigAttestationPolicy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contents    string
		expectError bool
		expectProd  string
	}{
		{
			name: "per-server required policy",
			contents: `{
				"servers": {
					"prod": {
						"repository": "https://prod.example.com",
						"toolsRepository": "https://prod.example.com/tools",
						"attestationPolicy": "required"
					}
				}
			}`,
			expectError: false,
			expectProd:  AttestationPolicyRequired,
		},
		{
			name: "per-server typo refused",
			contents: `{
				"servers": {
					"prod": {
						"repository": "https://prod.example.com",
						"attestationPolicy": "require"
					}
				}
			}`,
			expectError: true,
			expectProd:  "",
		},
		{
			name: "top-level typo refused",
			contents: `{
				"attestationPolicy": "optionl",
				"servers": {
					"prod": {
						"repository": "https://prod.example.com"
					}
				}
			}`,
			expectError: true,
			expectProd:  "",
		},
		{
			name: "legacy format with policy",
			contents: `{
				"attestationPolicy": "required",
				"dbt": {
					"repository": "https://legacy.example.com",
					"truststore": "https://legacy.example.com/truststore"
				},
				"tools": {
					"repository": "https://legacy.example.com/tools"
				}
			}`,
			expectError: false,
			expectProd:  "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			homedir := writeTempDbtConfig(t, tc.contents)

			config, err := LoadMultiServerConfig(homedir, false)
			if tc.expectError {
				assert.Error(t, err, "config with invalid attestation policy must be refused at load")

				return
			}

			require.NoError(t, err, "valid config must load")

			if tc.expectProd != "" {
				server := config.Servers["prod"]
				assert.Equal(t, tc.expectProd, server.AttestationPolicy, "per-server attestation policy must round-trip")
			}

			if tc.name == "legacy format with policy" {
				server := config.toLegacyServer()
				assert.Equal(t, AttestationPolicyRequired, server.AttestationPolicy, "legacy top-level policy must reach the server config")
			}
		})
	}
}

func TestMultiServerConfigToConfigAttestationPolicy(t *testing.T) {
	t.Parallel()

	t.Run("server policy wins over top-level", func(t *testing.T) {
		t.Parallel()

		multiConfig := MultiServerConfig{
			AttestationPolicy: AttestationPolicyOptional,
			Servers: map[string]ServerConfig{
				"prod": {
					Repository:        "https://prod.example.com",
					AttestationPolicy: AttestationPolicyRequired,
				},
			},
		}

		config := multiConfig.ToConfig(multiConfig.Servers["prod"])
		assert.Equal(t, AttestationPolicyRequired, config.AttestationPolicy, "server-level policy must take precedence")
	})

	t.Run("top-level policy used when server has none", func(t *testing.T) {
		t.Parallel()

		multiConfig := MultiServerConfig{
			AttestationPolicy: AttestationPolicyRequired,
			Servers: map[string]ServerConfig{
				"prod": {
					Repository: "https://prod.example.com",
				},
			},
		}

		config := multiConfig.ToConfig(multiConfig.Servers["prod"])
		assert.Equal(t, AttestationPolicyRequired, config.AttestationPolicy, "top-level policy must be the fallback")
	})
}

func TestFetchFileIfPresent(t *testing.T) {
	tests := []struct {
		name    string
		obj     *DBT
		url     string
		missing bool
	}{
		{
			"reposerver-existing-file",
			&DBT{
				Config:  dbtConfig,
				Verbose: true,
			},
			fmt.Sprintf("%s/%s/linux/amd64/%s", dbtConfig.Dbt.Repo, latestVersion, BrandBinary),
			false,
		},
		{
			"reposerver-missing-file",
			&DBT{
				Config:  dbtConfig,
				Verbose: true,
			},
			fmt.Sprintf("%s/%s/linux/amd64/%s.att.json", dbtConfig.Dbt.Repo, oldVersion, BrandBinary),
			true,
		},
		{
			"s3-existing-file",
			&DBT{
				Config:    s3DbtConfig,
				Verbose:   true,
				S3Session: s3Session,
			},
			fmt.Sprintf("%s/truststore", s3DbtConfig.Dbt.Repo),
			false,
		},
		{
			"s3-missing-file",
			&DBT{
				Config:    s3DbtConfig,
				Verbose:   true,
				S3Session: s3Session,
			},
			fmt.Sprintf("%s/%s/linux/amd64/%s.att.json", s3DbtConfig.Dbt.Repo, oldVersion, BrandBinary),
			true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			destPath := filepath.Join(t.TempDir(), "fetched")

			found, err := tc.obj.FetchFileIfPresent(tc.url, destPath)
			require.NoError(t, err, "fetch-if-present must distinguish absence from failure")

			if tc.missing {
				assert.False(t, found, "missing file must report found=false")
				assert.NoFileExists(t, destPath, "no file may be written for a missing artifact")

				return
			}

			assert.True(t, found, "existing file must report found=true")
			assert.FileExists(t, destPath, "existing file must be written to disk")
		})
	}
}

func TestFetchFileIfPresentConnectionFailure(t *testing.T) {
	// A dead endpoint must surface as an error, not as "absent" — confusing a broken
	// repository with a tool that simply lacks attestations would silently weaken
	// the optional policy from "verify what is there" to "verify nothing".
	obj := &DBT{
		Config: Config{
			Dbt: DbtConfig{
				Repo: "http://127.0.0.1:1/dbt",
			},
		},
		Verbose: true,
	}

	found, err := obj.FetchFileIfPresent("http://127.0.0.1:1/dbt/1.0.0/linux/amd64/dbt.att.json", filepath.Join(t.TempDir(), "fetched"))
	require.Error(t, err, "connection failure must be an error")
	assert.False(t, found, "failed fetch must not report found")
}

// writeTempTruststore writes the fixture truststore into a temp homedir where
// VerifyFileSignature expects it.
func writeTempTruststore(t *testing.T, homedir string) {
	t.Helper()

	trustDir := filepath.Join(homedir, filepath.Dir(TruststorePath))
	err := os.MkdirAll(trustDir, 0755)
	require.NoError(t, err, "failed creating temp trust dir")

	err = os.WriteFile(filepath.Join(homedir, TruststorePath), []byte(testfixtures.Truststore), 0644)
	require.NoError(t, err, "failed writing temp truststore")
}

// writeLocalCatalogTool writes the catalog 3.0.0 fixture (binary, checksum, signature,
// and optionally its attestations) into a temp homedir in the layout verifyAndRun
// expects, plus the fixture truststore. Returns the homedir and the local tool path.
func writeLocalCatalogTool(t *testing.T, withAttestations bool) (homedir string, toolPath string) {
	t.Helper()

	homedir = t.TempDir()

	toolDir := filepath.Join(homedir, ToolDir)
	err := os.MkdirAll(toolDir, 0755)
	require.NoError(t, err, "failed creating temp tool dir")

	toolPath = filepath.Join(toolDir, "catalog")

	err = os.WriteFile(toolPath, testfixtures.Catalog300Binary, 0755)
	require.NoError(t, err, "failed writing temp tool binary")

	err = os.WriteFile(fmt.Sprintf("%s.sha256", toolPath), []byte(strings.TrimSpace(testfixtures.Catalog300Checksum)), 0644)
	require.NoError(t, err, "failed writing temp checksum")

	err = os.WriteFile(fmt.Sprintf("%s.asc", toolPath), []byte(testfixtures.Catalog300Signature), 0644)
	require.NoError(t, err, "failed writing temp signature")

	if withAttestations {
		err = os.WriteFile(fmt.Sprintf("%s.att.json", toolPath), []byte(testfixtures.Catalog300Attestation), 0644)
		require.NoError(t, err, "failed writing temp attestation")

		err = os.WriteFile(fmt.Sprintf("%s.att.json.asc", toolPath), []byte(testfixtures.Catalog300AttestationSig), 0644)
		require.NoError(t, err, "failed writing temp attestation signature")

		err = os.WriteFile(fmt.Sprintf("%s.sbom.json", toolPath), []byte(testfixtures.Catalog300SBOM), 0644)
		require.NoError(t, err, "failed writing temp sbom")

		err = os.WriteFile(fmt.Sprintf("%s.sbom.json.asc", toolPath), []byte(testfixtures.Catalog300SBOMSig), 0644)
		require.NoError(t, err, "failed writing temp sbom signature")
	}

	writeTempTruststore(t, homedir)

	return homedir, toolPath
}

func TestVerifyAttestations(t *testing.T) {
	tests := []struct {
		name        string
		withAtt     bool
		mutate      func(t *testing.T, toolPath string)
		wantPresent bool
		wantOk      bool
	}{
		{
			"valid attestations pass",
			true,
			nil,
			true,
			true,
		},
		{
			"no attestation files means not present",
			false,
			nil,
			false,
			false,
		},
		{
			"attestation without sbom is broken",
			true,
			func(t *testing.T, toolPath string) {
				err := os.Remove(fmt.Sprintf("%s.sbom.json", toolPath))
				require.NoError(t, err, "failed removing sbom fixture")
			},
			true,
			false,
		},
		{
			"sbom without attestation is broken",
			true,
			func(t *testing.T, toolPath string) {
				err := os.Remove(fmt.Sprintf("%s.att.json", toolPath))
				require.NoError(t, err, "failed removing attestation fixture")
			},
			true,
			false,
		},
		{
			"missing attestation signature is broken",
			true,
			func(t *testing.T, toolPath string) {
				err := os.Remove(fmt.Sprintf("%s.att.json.asc", toolPath))
				require.NoError(t, err, "failed removing attestation signature fixture")
			},
			true,
			false,
		},
		{
			"tampered attestation is broken",
			true,
			func(t *testing.T, toolPath string) {
				err := os.WriteFile(fmt.Sprintf("%s.att.json", toolPath), []byte("{ tampered"), 0644)
				require.NoError(t, err, "failed tampering attestation fixture")
			},
			true,
			false,
		},
		{
			"attestation bound to different bytes is broken",
			true,
			func(t *testing.T, toolPath string) {
				// Replace the tool bytes: the attestation files' sigs still verify (they
				// are untouched), but the subject digest no longer matches the
				// tool on disk — exactly the swap the binding check exists to catch.
				err := os.WriteFile(toolPath, []byte("these are not the bytes that were attested"), 0755)
				require.NoError(t, err, "failed replacing tool bytes")
			},
			true,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			homedir, toolPath := writeLocalCatalogTool(t, tc.withAtt)

			if tc.mutate != nil {
				tc.mutate(t, toolPath)
			}

			obj := &DBT{Verbose: true}

			present, ok, err := obj.VerifyAttestations(homedir, toolPath, "catalog")

			// The contract: ok is the verdict. Absence (present=false) is not an error;
			// a broken verdict carries a diagnostic err the caller folds into its message.
			if tc.wantOk {
				require.NoError(t, err, "a passing verdict must carry no error")
			} else if tc.wantPresent {
				require.Error(t, err, "a broken verdict must carry a diagnostic error")
			} else {
				require.NoError(t, err, "absence is a policy question, not an error")
			}

			assert.Equal(t, tc.wantPresent, present, "presence")
			assert.Equal(t, tc.wantOk, ok, "verdict")
		})
	}
}

// TestHelperProcess is the exec target for runExec under test: with testExec enabled,
// runExec re-invokes the test binary with -test.run=TestHelperProcess and a "--" marker
// before the tool path. The marker distinguishes the child invocation from a normal
// suite run, where this test is a no-op.
func TestHelperProcess(t *testing.T) {
	for _, arg := range os.Args {
		if arg == "--" {
			fmt.Printf("TestHelperProcess: invoked as %v\n", os.Args)

			os.Exit(0)
		}
	}
}

func TestVerifyAndRunAttestationPolicy(t *testing.T) {
	tests := []struct {
		name       string
		policy     string
		withAtt    bool
		tamper     bool
		expectRuns bool
	}{
		{
			"required with valid attestations runs",
			AttestationPolicyRequired,
			true,
			false,
			true,
		},
		{
			"optional with valid attestations runs",
			AttestationPolicyOptional,
			true,
			false,
			true,
		},
		{
			"optional without attestations runs",
			AttestationPolicyOptional,
			false,
			false,
			true,
		},
		{
			"required without attestations refuses",
			AttestationPolicyRequired,
			false,
			false,
			false,
		},
		{
			"optional with tampered attestation refuses",
			AttestationPolicyOptional,
			true,
			true,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testExec = true

			t.Cleanup(func() {
				testExec = false
			})

			homedir, toolPath := writeLocalCatalogTool(t, tc.withAtt)

			if tc.tamper {
				err := os.WriteFile(fmt.Sprintf("%s.att.json", toolPath), []byte("{ tampered"), 0644)
				require.NoError(t, err, "failed tampering attestation fixture")
			}

			obj := &DBT{
				Config:  Config{AttestationPolicy: tc.policy},
				Verbose: true,
			}

			err := obj.verifyAndRun(homedir, []string{"catalog"})
			if tc.expectRuns {
				require.NoError(t, err, "valid tool must run")
			} else {
				require.Error(t, err, "tool must be refused")
			}
		})
	}
}

func TestRunToolDownloadsAttestations(t *testing.T) {
	testExec = true

	t.Cleanup(func() {
		testExec = false
	})

	homedir := t.TempDir()
	writeTempTruststore(t, homedir)

	obj := &DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)}

	err := obj.RunTool("", []string{"catalog"}, homedir, false)
	require.NoError(t, err, "runtool on attested fixture tool must succeed")

	toolPath := filepath.Join(homedir, ToolDir, "catalog")

	for _, suffix := range []string{".att.json", ".att.json.asc", ".sbom.json", ".sbom.json.asc"} {
		assert.FileExists(t, fmt.Sprintf("%s%s", toolPath, suffix), "attestation files must be cached beside the tool")
	}
}

func TestRunToolRequiredPolicyRefusesUnattestedVersion(t *testing.T) {
	homedir := t.TempDir()

	config := dbtConfig
	config.AttestationPolicy = AttestationPolicyRequired

	obj := &DBT{Config: config, Verbose: true, Logger: log.New(os.Stderr, "", 0)}

	// catalog 2.0.0 is the fixture version published without attestations.
	err := obj.RunTool("2.0.0", []string{"catalog"}, homedir, false)
	require.Error(t, err, "required policy must refuse a version without attestations")
}

func TestRunToolRequiredPolicyRefusesUnattestedVersionS3(t *testing.T) {
	homedir := t.TempDir()

	config := s3DbtConfig
	config.AttestationPolicy = AttestationPolicyRequired

	obj := &DBT{Config: config, S3Session: s3Session, Verbose: true, Logger: log.New(os.Stderr, "", 0)}

	err := obj.RunTool("2.0.0", []string{"catalog"}, homedir, false)
	require.Error(t, err, "required policy must refuse a version without attestations over S3")
}

func TestCheckUpgradeAttestations(t *testing.T) {
	tests := []struct {
		name      string
		obj       *DBT
		homedir   string
		version   string
		policy    string
		expectErr bool
	}{
		{
			"reposerver optional with attested latest passes",
			&DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
			"",
			latestVersion,
			AttestationPolicyOptional,
			false,
		},
		{
			"reposerver required with attested latest passes",
			&DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
			"",
			latestVersion,
			AttestationPolicyRequired,
			false,
		},
		{
			"reposerver required with unattested version refuses",
			&DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
			"",
			newVersion,
			AttestationPolicyRequired,
			true,
		},
		{
			"reposerver optional with unattested version passes",
			&DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
			"",
			newVersion,
			AttestationPolicyOptional,
			false,
		},
		{
			"s3 required with attested latest passes",
			&DBT{Config: s3DbtConfig, S3Session: s3Session, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
			"",
			latestVersion,
			AttestationPolicyRequired,
			false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			obj := tc.obj
			obj.Config.AttestationPolicy = tc.policy

			homedir := t.TempDir()
			writeTempTruststore(t, homedir)

			workDir := t.TempDir()
			stagedPath := filepath.Join(workDir, BrandBinary)

			// Stage the incoming binary exactly as UpgradeInPlace does.
			versionURL := fmt.Sprintf("%s/%s/%s/%s/%s", obj.Config.Dbt.Repo, tc.version, runtime.GOOS, runtime.GOARCH, BrandBinary)
			err := obj.FetchFile(versionURL, stagedPath)
			require.NoError(t, err, "failed staging fixture binary")

			err = obj.checkUpgradeAttestations(homedir, versionURL, stagedPath)
			if tc.expectErr {
				require.Error(t, err, "upgrade attestations must refuse")
			} else {
				require.NoError(t, err, "upgrade attestations must pass")
			}
		})
	}
}

func TestRunToolStaleAttestationsDoNotRefuseOlderVersion(t *testing.T) {
	tests := []struct {
		name string
		obj  *DBT
	}{
		{
			"reposerver",
			&DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
		},
		{
			"s3",
			&DBT{Config: s3DbtConfig, S3Session: s3Session, Verbose: true, Logger: log.New(os.Stderr, "", 0)},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			testExec = true

			t.Cleanup(func() {
				testExec = false
			})

			homedir := t.TempDir()
			writeTempTruststore(t, homedir)

			// First run: latest (attested). The attestation files get cached beside the tool.
			err := tc.obj.RunTool("", []string{"catalog"}, homedir, false)
			require.NoError(t, err, "attested latest version must run")

			toolPath := filepath.Join(homedir, ToolDir, "catalog")

			for _, suffix := range []string{".att.json", ".att.json.asc", ".sbom.json", ".sbom.json.asc"} {
				assert.FileExists(t, fmt.Sprintf("%s%s", toolPath, suffix), "attested run must cache the attestation files")
			}

			// Second run: pin the unattested older version under the default
			// (optional) policy. The stale attestation files from the latest version must
			// not survive to fail digest binding against these bytes: the fetch
			// path removes local attestation files the repository does not carry.
			err = tc.obj.RunTool(newVersion, []string{"catalog"}, homedir, false)
			require.NoError(t, err, "pinning an unattested version must not be refused by stale attestation files")

			for _, suffix := range []string{".att.json", ".att.json.asc", ".sbom.json", ".sbom.json.asc"} {
				assert.NoFileExists(t, fmt.Sprintf("%s%s", toolPath, suffix), "stale attestation files must be removed when the repo carries none")
			}
		})
	}
}

func TestFetchAttestationsRemovesStaleCopies(t *testing.T) {
	obj := &DBT{Config: dbtConfig, Verbose: true, Logger: log.New(os.Stderr, "", 0)}

	toolURL := fmt.Sprintf("%s/catalog/%s/linux/amd64/catalog", dbtConfig.Tools.Repo, newVersion)
	localPath := filepath.Join(t.TempDir(), "catalog")

	// Simulate a stale attestation file left behind by a previous, attested version.
	stalePath := fmt.Sprintf("%s.att.json", localPath)
	err := os.WriteFile(stalePath, []byte("stale attestation"), 0644)
	require.NoError(t, err, "failed writing stale attestation file")

	// The repository publishes no attestations for this version; the stale local copy
	// must not survive the fetch.
	err = obj.FetchAttestations(toolURL, localPath, "catalog")
	require.NoError(t, err, "absent remote attestations under optional policy are not an error")

	assert.NoFileExists(t, stalePath, "stale local attestation file must be removed when the repo carries none")
}

func TestVerifyAttestationsOperationalStatError(t *testing.T) {
	homedir, toolPath := writeLocalCatalogTool(t, true)

	// A self-referential symlink makes stat return ELOOP: neither absent (ENOENT) nor
	// success. This pins the operational-error contract: present=false with a non-nil
	// error, which callers must treat as fatal regardless of policy.
	attPath := fmt.Sprintf("%s.att.json", toolPath)
	err := os.Remove(attPath)
	require.NoError(t, err, "failed removing fixture attestation")

	err = os.Symlink(attPath, attPath)
	require.NoError(t, err, "failed creating self-referential symlink")

	obj := &DBT{Verbose: true}

	present, ok, attErr := obj.VerifyAttestations(homedir, toolPath, "catalog")
	assert.False(t, present, "operational failure must not be reported as presence")
	assert.False(t, ok, "operational failure must not be reported as a pass")
	require.Error(t, attErr, "operational failure must surface as an error")
}

func TestVerifyAndRunOperationalStatErrorRefuses(t *testing.T) {
	testExec = true

	t.Cleanup(func() {
		testExec = false
	})

	homedir, toolPath := writeLocalCatalogTool(t, true)

	// ELOOP on the attestation stat: an operational failure in the third leg. Checksum
	// and signature still verify — the binary and its sigs are untouched — so the only
	// thing standing between this run and execution is the caller treating the third
	// leg's error as fatal.
	attPath := fmt.Sprintf("%s.att.json", toolPath)
	err := os.Remove(attPath)
	require.NoError(t, err, "failed removing fixture attestation")

	err = os.Symlink(attPath, attPath)
	require.NoError(t, err, "failed creating self-referential symlink")

	obj := &DBT{
		Config:  Config{AttestationPolicy: AttestationPolicyOptional},
		Verbose: true,
	}

	err = obj.verifyAndRun(homedir, []string{"catalog"})
	require.Error(t, err, "an operational error in the third leg must refuse the run, not fail open")
}
