// openvox-enc is a Puppet External Node Classifier (ENC) script that queries
// an external classification service and returns YAML for Puppet Server.
//
// Usage (called by Puppet as ENC):
//
//	openvox-enc --config /path/to/enc.yaml <certname>
//
// Exit 0: classification YAML on stdout.
// Exit 1: node not found (HTTP 404).
// Exit 2: error (network, config, parse).
package main

import (
	"flag"
	"fmt"
	"os"
)

const defaultConfigPath = "/etc/puppetlabs/puppet/enc.yaml"

func main() {
	configPath := flag.String("config", defaultConfigPath, "Path to ENC config YAML")
	flag.Parse()

	args := flag.Args()
	if len(args) < 1 {
		fmt.Fprintln(os.Stderr, "error: certname argument is required")
		os.Exit(2)
	}
	certname := args[0]

	cfg, err := loadENCConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	result, err := classify(cfg, certname)
	if err != nil {
		// Try cache fallback
		if cfg.Cache.Enabled {
			cached, cacheErr := readCache(cfg.Cache.Directory, certname)
			if cacheErr == nil {
				fmt.Print(cached)
				os.Exit(0)
			}
		}

		if isNotFound(err) {
			fmt.Fprintf(os.Stderr, "error: node not found: %s\n", certname)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(2)
	}

	// Save to cache
	if cfg.Cache.Enabled {
		_ = saveCache(cfg.Cache.Directory, certname, result)
	}

	fmt.Print(result)
}
