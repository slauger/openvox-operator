package main

import (
	"encoding/json"
	"testing"
)

// sampleReport returns a to_data_hash report for testing.
func sampleReport() map[string]any {
	return map[string]any{
		"host":                   "testnode.example.com",
		"time":                   "2024-01-15T10:30:00.000000000Z",
		"configuration_version":  "1705312200",
		"transaction_uuid":       "abc-123",
		"report_format":          float64(10),
		"puppet_version":         "8.4.0",
		"status":                 "changed",
		"transaction_completed":  true,
		"noop":                   false,
		"noop_pending":           false,
		"environment":            "production",
		"corrective_change":      false,
		"catalog_uuid":           "cat-456",
		"code_id":                "code-789",
		"job_id":                 "job-012",
		"cached_catalog_status":  "not_used",
		"server_used":            "puppet.example.com",
		"resource_statuses": map[string]any{
			"Notify[hello]": map[string]any{
				"title":             "hello",
				"file":              "/etc/puppetlabs/code/environments/production/manifests/site.pp",
				"line":              float64(1),
				"resource":          "Notify[hello]",
				"resource_type":     "Notify",
				"containment_path":  []any{"Stage[main]", "Main", "Notify[hello]"},
				"time":              "2024-01-15T10:30:01.000000000Z",
				"skipped":           false,
				"corrective_change": false,
				"events": []any{
					map[string]any{
						"status":            "success",
						"time":              "2024-01-15T10:30:01.500000000Z",
						"name":              "message",
						"property":          "message",
						"desired_value":     "hello world",
						"previous_value":    "absent",
						"corrective_change": false,
						"message":           "defined 'message' as 'hello world'",
					},
				},
			},
		},
		"metrics": map[string]any{
			"time": map[string]any{
				"name":   "time",
				"label":  "Time",
				"values": []any{
					[]any{"total", "Total", float64(5.5)},
					[]any{"notify", "Notify", float64(0.001)},
				},
			},
			"resources": map[string]any{
				"name":   "resources",
				"label":  "Resources",
				"values": []any{
					[]any{"total", "Total", float64(1)},
					[]any{"changed", "Changed", float64(1)},
				},
			},
		},
		"logs": []any{
			map[string]any{
				"level":   "notice",
				"message": "hello world",
				"source":  "Notify[hello]/message",
				"tags":    []any{"notice", "notify", "hello"},
				"time":    "2024-01-15T10:30:01.000000000Z",
				"file":    "/etc/puppetlabs/code/environments/production/manifests/site.pp",
				"line":    float64(1),
			},
		},
	}
}

func TestTransformToPuppetDB(t *testing.T) {
	report := sampleReport()
	reportJSON, err := json.Marshal(report)
	if err != nil {
		t.Fatalf("marshaling test report: %v", err)
	}

	resultJSON, err := transformToPuppetDB(reportJSON)
	if err != nil {
		t.Fatalf("transformToPuppetDB: %v", err)
	}

	var cmd PuppetDBCommand
	if err := json.Unmarshal(resultJSON, &cmd); err != nil {
		t.Fatalf("unmarshaling result: %v", err)
	}

	if cmd.Command != "store report" {
		t.Errorf("command = %q, want %q", cmd.Command, "store report")
	}
	if cmd.Version != 8 {
		t.Errorf("version = %d, want 8", cmd.Version)
	}

	payload, ok := cmd.Payload.(map[string]any)
	if !ok {
		t.Fatal("payload is not a map")
	}

	// host → certname
	if payload["certname"] != "testnode.example.com" {
		t.Errorf("certname = %v, want testnode.example.com", payload["certname"])
	}

	// time → start_time
	if payload["start_time"] != "2024-01-15T10:30:00.000000000Z" {
		t.Errorf("start_time = %v", payload["start_time"])
	}

	// end_time should be start_time + 5.5s
	if payload["end_time"] != "2024-01-15T10:30:05.5Z" {
		t.Errorf("end_time = %v, want 2024-01-15T10:30:05.5Z", payload["end_time"])
	}

	// producer from server_used
	if payload["producer"] != "puppet.example.com" {
		t.Errorf("producer = %v, want puppet.example.com", payload["producer"])
	}

	// producer_timestamp should be set
	if payload["producer_timestamp"] == nil || payload["producer_timestamp"] == "" {
		t.Error("producer_timestamp should be set")
	}

	// Passthrough fields
	if payload["status"] != "changed" {
		t.Errorf("status = %v, want changed", payload["status"])
	}
	if payload["environment"] != "production" {
		t.Errorf("environment = %v, want production", payload["environment"])
	}
	if payload["catalog_uuid"] != "cat-456" {
		t.Errorf("catalog_uuid = %v, want cat-456", payload["catalog_uuid"])
	}
	if payload["noop"] != false {
		t.Errorf("noop = %v, want false", payload["noop"])
	}

	// Fields that should NOT be in wire format
	for _, field := range []string{"host", "time", "transaction_completed", "server_used", "resource_statuses"} {
		if _, has := payload[field]; has {
			t.Errorf("payload should not contain %q", field)
		}
	}

	// logs pass through
	logs, ok := payload["logs"].([]any)
	if !ok || len(logs) != 1 {
		t.Fatal("logs should have 1 entry")
	}
}

func TestTransformResources(t *testing.T) {
	report := sampleReport()
	reportJSON, _ := json.Marshal(report)
	resultJSON, _ := transformToPuppetDB(reportJSON)

	var cmd PuppetDBCommand
	if err := json.Unmarshal(resultJSON, &cmd); err != nil {
		t.Fatal(err)
	}
	payload := cmd.Payload.(map[string]any)

	resources, ok := payload["resources"].([]any)
	if !ok {
		t.Fatal("resources should be an array")
	}
	if len(resources) != 1 {
		t.Fatalf("resources count = %d, want 1", len(resources))
	}

	resource := resources[0].(map[string]any)

	// title → resource_title
	if resource["resource_title"] != "hello" {
		t.Errorf("resource_title = %v, want hello", resource["resource_title"])
	}

	// time → timestamp
	if resource["timestamp"] != "2024-01-15T10:30:01.000000000Z" {
		t.Errorf("resource timestamp = %v", resource["timestamp"])
	}

	if resource["resource_type"] != "Notify" {
		t.Errorf("resource_type = %v, want Notify", resource["resource_type"])
	}

	// Should not have to_data_hash-specific field names
	if _, has := resource["title"]; has {
		t.Error("resource should not contain 'title' (should be 'resource_title')")
	}
	if _, has := resource["time"]; has {
		t.Error("resource should not contain 'time' (should be 'timestamp')")
	}
}

func TestTransformEvents(t *testing.T) {
	report := sampleReport()
	reportJSON, _ := json.Marshal(report)
	resultJSON, _ := transformToPuppetDB(reportJSON)

	var cmd PuppetDBCommand
	if err := json.Unmarshal(resultJSON, &cmd); err != nil {
		t.Fatal(err)
	}
	payload := cmd.Payload.(map[string]any)
	resources := payload["resources"].([]any)
	resource := resources[0].(map[string]any)
	events := resource["events"].([]any)

	if len(events) != 1 {
		t.Fatalf("events count = %d, want 1", len(events))
	}

	event := events[0].(map[string]any)

	// desired_value → new_value
	if event["new_value"] != "hello world" {
		t.Errorf("new_value = %v, want 'hello world'", event["new_value"])
	}

	// previous_value → old_value
	if event["old_value"] != "absent" {
		t.Errorf("old_value = %v, want 'absent'", event["old_value"])
	}

	// time → timestamp
	if event["timestamp"] != "2024-01-15T10:30:01.500000000Z" {
		t.Errorf("event timestamp = %v", event["timestamp"])
	}

	// Should not have to_data_hash-specific field names
	for _, field := range []string{"desired_value", "previous_value", "time"} {
		if _, has := event[field]; has {
			t.Errorf("event should not contain %q", field)
		}
	}
}

func TestTransformMetrics(t *testing.T) {
	report := sampleReport()
	reportJSON, _ := json.Marshal(report)
	resultJSON, _ := transformToPuppetDB(reportJSON)

	var cmd PuppetDBCommand
	if err := json.Unmarshal(resultJSON, &cmd); err != nil {
		t.Fatal(err)
	}
	payload := cmd.Payload.(map[string]any)

	metrics, ok := payload["metrics"].([]any)
	if !ok {
		t.Fatal("metrics should be an array")
	}

	// 2 from "time" + 2 from "resources" = 4
	if len(metrics) != 4 {
		t.Errorf("metrics count = %d, want 4", len(metrics))
	}

	foundTimeTotal := false
	for _, m := range metrics {
		mMap := m.(map[string]any)
		if mMap["category"] == "time" && mMap["name"] == "total" {
			foundTimeTotal = true
			if mMap["value"] != float64(5.5) {
				t.Errorf("time.total value = %v, want 5.5", mMap["value"])
			}
		}
	}
	if !foundTimeTotal {
		t.Error("should have time.total metric")
	}
}

func TestProducerFallbackToHost(t *testing.T) {
	report := map[string]any{
		"host":    "node.example.com",
		"time":    "2024-01-15T10:30:00.000000000Z",
		"metrics": map[string]any{},
	}

	reportJSON, _ := json.Marshal(report)
	resultJSON, err := transformToPuppetDB(reportJSON)
	if err != nil {
		t.Fatalf("transformToPuppetDB: %v", err)
	}

	var cmd PuppetDBCommand
	if err := json.Unmarshal(resultJSON, &cmd); err != nil {
		t.Fatalf("json.Unmarshal: %v", err)
	}
	payload := cmd.Payload.(map[string]any)

	if payload["producer"] != "node.example.com" {
		t.Errorf("producer = %v, want node.example.com (fallback to host)", payload["producer"])
	}
}

func TestEventNilValues(t *testing.T) {
	events := []any{
		map[string]any{
			"status":            "success",
			"time":              "2024-01-15T10:30:01.000Z",
			"name":              "ensure",
			"property":          "ensure",
			"desired_value":     nil,
			"previous_value":    nil,
			"corrective_change": false,
			"message":           "created",
		},
	}

	result := transformEvents(events)
	if len(result) != 1 {
		t.Fatalf("expected 1 event, got %d", len(result))
	}

	event := result[0].(map[string]any)
	if event["new_value"] != "" {
		t.Errorf("new_value for nil should be empty string, got %q", event["new_value"])
	}
	if event["old_value"] != "" {
		t.Errorf("old_value for nil should be empty string, got %q", event["old_value"])
	}
}

func TestEndTimeWithoutMetrics(t *testing.T) {
	report := map[string]any{
		"host":    "node.example.com",
		"time":    "2024-01-15T10:30:00.000000000Z",
		"metrics": map[string]any{},
	}

	endTime := calculateEndTime(report)

	// Without metrics, end_time = start_time + 0 = start_time
	if endTime != "2024-01-15T10:30:00Z" {
		t.Errorf("end_time = %v, want 2024-01-15T10:30:00Z", endTime)
	}
}

func TestEmptyResourceStatuses(t *testing.T) {
	result := transformResources(map[string]any{})
	if len(result) != 0 {
		t.Errorf("expected empty resources, got %d", len(result))
	}

	result = transformResources(nil)
	if len(result) != 0 {
		t.Errorf("expected empty resources for nil, got %d", len(result))
	}
}

func TestEmptyMetrics(t *testing.T) {
	result := transformMetrics(map[string]any{})
	if len(result) != 0 {
		t.Errorf("expected empty metrics, got %d", len(result))
	}

	result = transformMetrics(nil)
	if len(result) != 0 {
		t.Errorf("expected empty metrics for nil, got %d", len(result))
	}
}
