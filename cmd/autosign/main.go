// openvox-autosign is a Puppet autosign script that evaluates signing policies.
//
// Usage (called by Puppet as autosign script):
//
//	openvox-autosign --config /path/to/autosign-policy.yaml <certname>
//
// The CSR is read from stdin (PEM-encoded). Exit 0 to sign, exit 1 to deny.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/slauger/openvox-operator/internal/puppet"
)

func main() {
	configPath := flag.String("config", puppet.AutosignPolicyPath, "Path to autosign policy YAML config")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: certname argument is required")
		os.Exit(1)
	}
	certname := args[0]

	// Load policy config
	cfg, err := loadPolicyConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Read CSR from stdin
	csrPEM, err := io.ReadAll(os.Stdin)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error reading CSR from stdin: %v\n", err)
		os.Exit(1)
	}

	csr, err := parseCSR(csrPEM)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error parsing CSR: %v\n", err)
		os.Exit(1)
	}

	// Evaluate policies (OR between policies)
	if evaluatePolicies(cfg, certname, csr) {
		os.Exit(0)
	}

	os.Exit(1)
}
