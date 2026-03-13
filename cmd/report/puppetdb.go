package main

import (
	"encoding/json"
	"fmt"
)

// PuppetDBCommand is the PuppetDB Wire Format v8 command envelope.
type PuppetDBCommand struct {
	Command string      `json:"command"`
	Version int         `json:"version"`
	Payload interface{} `json:"payload"`
}

// transformToPuppetDB wraps a Puppet report in PuppetDB Wire Format v8 command envelope.
func transformToPuppetDB(reportJSON []byte) ([]byte, error) {
	var report map[string]interface{}
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		return nil, fmt.Errorf("parsing report JSON: %w", err)
	}

	cmd := PuppetDBCommand{
		Command: "store report",
		Version: 8,
		Payload: report,
	}

	return json.Marshal(cmd)
}
