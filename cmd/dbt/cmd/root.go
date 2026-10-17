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

package cmd

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"
	"syscall"

	"github.com/nikogura/dynamic-binary-toolkit/pkg/dbt"
	"github.com/spf13/cobra"
)

//nolint:gochecknoglobals // Cobra requires global variables for flags
var toolVersion string

//nolint:gochecknoglobals // Cobra requires global variables for flags
var offline bool

//nolint:gochecknoglobals // Cobra requires global variables for flags
var verbose bool

//nolint:gochecknoglobals // Cobra requires global variables for flags
var serverFlag string

//nolint:gochecknoglobals // Cobra requires global variables for flags
var authCheck bool

//nolint:gochecknoglobals // Cobra boilerplate
var rootCmd = &cobra.Command{
	Use:   dbt.BrandBinary,
	Short: dbt.BrandName + " - Dynamic Binary Toolkit",
	Long: dbt.BrandName + ` - Dynamic Binary Toolkit

A framework for running self-updating signed binaries from a trusted repository.

Run '` + dbt.BrandBinary + ` -- catalog list' to see a list of what tools are available in your repository.

Use -s/--server to select a specific server when multiple servers are configured.
Server selection priority: CLI flag (-s) > environment variable (` + dbt.GetServerEnvVar() + `) > config default > first server.

`,
	Example: dbt.BrandBinary + " -s prod -- catalog list",
	Version: dbt.VERSION,
	Run:     Run,
}

//nolint:gochecknoinits // Cobra boilerplate
func init() {
	rootCmd.Flags().StringVarP(&toolVersion, "toolversion", "v", "", "Version of tool to run.")
	rootCmd.Flags().BoolVarP(&offline, "offline", "o", false, "Offline mode.")
	rootCmd.Flags().BoolVarP(&verbose, "verbose", "V", false, "Verbose output")
	rootCmd.Flags().StringVarP(&serverFlag, "server", "s", "", "Server name to use (overrides DBT_SERVER env and config default)")
	rootCmd.Flags().BoolVar(&authCheck, "auth-check", false, "Check OIDC authentication status and exit")
}

// Execute executes the root command.
func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}

// Run run dbt itself.
func Run(cmd *cobra.Command, args []string) {
	// Handle --auth-check flag
	if authCheck {
		runAuthCheck()
		return
	}

	if len(args) == 0 {
		helpErr := cmd.Help()
		if helpErr != nil {
			log.Fatal(helpErr)
		}
		os.Exit(0)
	}

	dbtObj, serverName, err := dbt.NewDbtWithServer("", serverFlag)
	if err != nil {
		log.Fatalf("Error creating DBT object: %s", err)
	}

	if verbose && serverFlag != "" {
		log.Printf("Using server: %s", serverName)
	}

	dbtObj.SetVerbose(verbose)

	homedir, err := dbt.GetHomeDir()
	if err != nil {
		log.Fatalf("Failed to discover user homedir: %s\n", err)
	}

	dbtBinary, err := exec.LookPath(dbt.BrandBinary)
	if err != nil {
		log.Fatalf("Couldn't find `%s` in $PATH: %s", dbt.BrandBinary, err)
	}

	// if we're not explicitly offline, try to upgrade in place
	//nolint:nestif // upgrade flow requires nested checks
	if !offline {
		// first fetch the current truststore
		err = dbtObj.FetchTrustStore(homedir)
		if err != nil {
			log.Fatalf("Failed to fetch remote truststore: %s.\n\nIf you want to try in 'offline' mode, retry your command again with: %s -o ...", err, dbt.BrandBinary)
		}

		ok, currentErr := dbtObj.IsCurrent(dbtBinary)
		if currentErr != nil {
			log.Printf("Failed to confirm whether we're up to date: %s", currentErr)
		}

		if !ok {
			log.Printf("Downloading and verifying new version of %s.", dbt.BrandName)
			upgradeErr := dbtObj.UpgradeInPlace(homedir, dbtBinary)
			if upgradeErr != nil {
				upgradeErr = fmt.Errorf("upgrade in place failed: %w", upgradeErr)
				log.Fatalf("Error: %s", upgradeErr)
			}

			// Single white female ourself
			_ = syscall.Exec(dbtBinary, os.Args, os.Environ())
		}
	}

	err = dbtObj.RunTool(toolVersion, args, homedir, offline)
	if err != nil {
		log.Fatal(err)
	}
}

// runAuthCheck checks OIDC authentication status for the selected server.
func runAuthCheck() {
	dbtObj, serverName, err := dbt.NewDbtWithServer("", serverFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error creating DBT object: %s\n", err)
		os.Exit(1)
	}

	fmt.Printf("Server: %s\n", serverName)
	fmt.Printf("Repository: %s\n", dbtObj.Config.Dbt.Repo)

	// Check if OIDC is configured
	if dbtObj.OIDCClient == nil {
		fmt.Println("\nOIDC authentication is not configured for this server.")
		fmt.Println("AuthType in config must be 'oidc' with issuerUrl and oidcAudience set.")
		os.Exit(0)
	}

	fmt.Printf("Issuer URL: %s\n", dbtObj.OIDCClient.IssuerURL)
	fmt.Printf("Audience: %s\n", dbtObj.OIDCClient.Audience)
	fmt.Printf("Client ID: %s\n", dbtObj.OIDCClient.ClientID)
	fmt.Printf("Connector ID: %s\n", dbtObj.OIDCClient.ConnectorID)
	fmt.Println()

	// Attempt to get token
	fmt.Println("Attempting OIDC authentication...")
	token, tokenErr := dbtObj.OIDCClient.GetToken(context.Background())
	if tokenErr != nil {
		fmt.Fprintf(os.Stderr, "\n✗ Authentication failed: %s\n", tokenErr)
		os.Exit(1)
	}

	fmt.Println("✓ Authentication successful")
	fmt.Println()

	// Decode and display token claims
	claims, decodeErr := decodeJWTClaims(token)
	if decodeErr != nil {
		fmt.Printf("Token received but could not decode claims: %s\n", decodeErr)
		return
	}

	// Display relevant claims
	if sub, ok := claims["sub"].(string); ok {
		fmt.Printf("  Subject: %s\n", sub)
	}
	if name, ok := claims["name"].(string); ok {
		fmt.Printf("  Name: %s\n", name)
	}
	if email, ok := claims["email"].(string); ok {
		fmt.Printf("  Email: %s\n", email)
	}
	if groups, ok := claims["groups"].([]interface{}); ok {
		groupStrs := make([]string, 0, len(groups))
		for _, g := range groups {
			if gs, isStr := g.(string); isStr {
				groupStrs = append(groupStrs, gs)
			}
		}
		fmt.Printf("  Groups: %v\n", groupStrs)
	}
	if aud, ok := claims["aud"].(string); ok {
		fmt.Printf("  Audience: %s\n", aud)
	}
	if iss, ok := claims["iss"].(string); ok {
		fmt.Printf("  Issuer: %s\n", iss)
	}
}

// decodeJWTClaims extracts the claims from a JWT token without verification.
func decodeJWTClaims(token string) (claims map[string]interface{}, err error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		err = fmt.Errorf("invalid JWT format: expected 3 parts, got %d", len(parts))
		return claims, err
	}

	// Decode the payload (second part)
	payload := parts[1]
	// Add padding if needed
	switch len(payload) % 4 {
	case 2:
		payload += "=="
	case 3:
		payload += "="
	}

	decoded, decodeErr := base64.URLEncoding.DecodeString(payload)
	if decodeErr != nil {
		err = fmt.Errorf("failed to decode JWT payload: %w", decodeErr)
		return claims, err
	}

	claims = make(map[string]interface{})
	unmarshalErr := json.Unmarshal(decoded, &claims)
	if unmarshalErr != nil {
		err = fmt.Errorf("failed to parse JWT claims: %w", unmarshalErr)
		return claims, err
	}

	return claims, err
}
