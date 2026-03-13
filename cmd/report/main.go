// openvox-report is a Puppet report processor that forwards reports to external
// endpoints via HTTP webhooks.
//
// Usage (called by Puppet via webhook.rb):
//
//	openvox-report --config /path/to/report-webhook.yaml < report.json
//
// Exit 0: success (all endpoints received the report).
// Exit 2: error (config, network, or endpoint failure).
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

const defaultConfigPath = "/etc/puppetlabs/puppet/report-webhook.yaml"

func main() {
	configPath := flag.String("config", defaultConfigPath, "Path to report config YAML")
	flag.Parse()

	cfg, err := loadReportConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	// Read report JSON from stdin
	reportJSON, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: reading report from stdin: %v\n", err)
		os.Exit(2)
	}

	if len(reportJSON) == 0 {
		fmt.Fprintln(os.Stderr, "error: empty report received")
		os.Exit(2)
	}

	// Forward report to all configured endpoints
	var errors []error
	for _, endpoint := range cfg.Endpoints {
		if err := forward(endpoint, reportJSON); err != nil {
			errors = append(errors, fmt.Errorf("endpoint %s: %w", endpoint.Name, err))
		}
	}

	if len(errors) > 0 {
		for _, err := range errors {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(2)
	}

	fmt.Printf("report forwarded to %d endpoint(s)\n", len(cfg.Endpoints))
}
