package main

import (
	"encoding/json"
	"fmt"
	"time"
)

// PuppetDBCommand is the PuppetDB Wire Format v8 command envelope.
type PuppetDBCommand struct {
	Command string `json:"command"`
	Version int    `json:"version"`
	Payload any    `json:"payload"`
}

// transformToPuppetDB converts a Puppet report (to_data_hash format) into
// a PuppetDB Wire Format v8 command envelope.
//
// Key transformations from to_data_hash → Wire Format v8:
//   - host → certname
//   - time → start_time, end_time (calculated from metrics["time"]["total"])
//   - resource_statuses (map) → resources (array)
//   - metrics (nested {name, label, values}) → flat array [{category, name, value}]
//   - events: time→timestamp, desired_value→new_value, previous_value→old_value
//   - producer_timestamp and producer added
func transformToPuppetDB(reportJSON []byte) ([]byte, error) {
	var report map[string]any
	if err := json.Unmarshal(reportJSON, &report); err != nil {
		return nil, fmt.Errorf("parsing report JSON: %w", err)
	}

	wireReport := make(map[string]any)

	// host → certname
	wireReport["certname"] = report["host"]

	// time → start_time
	wireReport["start_time"] = report["time"]

	// end_time = start_time + run_duration (from metrics)
	wireReport["end_time"] = calculateEndTime(report)

	// producer_timestamp = now (matches official puppetdb.rb behavior)
	wireReport["producer_timestamp"] = time.Now().UTC().Format(time.RFC3339Nano)

	// producer = server_used (Puppet Server certname) or host as fallback
	if serverUsed, _ := report["server_used"].(string); serverUsed != "" {
		wireReport["producer"] = serverUsed
	} else {
		wireReport["producer"] = report["host"]
	}

	// Fields that pass through unchanged
	for _, field := range []string{
		"puppet_version", "report_format", "configuration_version",
		"environment", "transaction_uuid", "status", "noop", "noop_pending",
		"corrective_change", "catalog_uuid", "code_id",
		"cached_catalog_status", "job_id",
	} {
		if v, ok := report[field]; ok {
			wireReport[field] = v
		}
	}

	// resource_statuses (map) → resources (array)
	wireReport["resources"] = transformResources(report["resource_statuses"])

	// metrics (nested hash) → flat array
	wireReport["metrics"] = transformMetrics(report["metrics"])

	// logs pass through (format is compatible)
	wireReport["logs"] = report["logs"]

	cmd := PuppetDBCommand{
		Command: "store report",
		Version: 8,
		Payload: wireReport,
	}

	return json.Marshal(cmd)
}

// calculateEndTime computes end_time from start_time + metrics["time"]["total"].
func calculateEndTime(report map[string]any) string {
	startTimeStr, _ := report["time"].(string)
	if startTimeStr == "" {
		return ""
	}

	startTime, err := time.Parse(time.RFC3339Nano, startTimeStr)
	if err != nil {
		return startTimeStr
	}

	duration := getRunDuration(report)
	endTime := startTime.Add(time.Duration(duration * float64(time.Second)))
	return endTime.Format(time.RFC3339Nano)
}

// getRunDuration extracts the "total" run duration from metrics["time"]["values"].
func getRunDuration(report map[string]any) float64 {
	metrics, ok := report["metrics"].(map[string]any)
	if !ok {
		return 0
	}

	timeMetric, ok := metrics["time"].(map[string]any)
	if !ok {
		return 0
	}

	values, ok := timeMetric["values"].([]any)
	if !ok {
		return 0
	}

	for _, v := range values {
		tuple, ok := v.([]any)
		if !ok || len(tuple) < 3 {
			continue
		}
		if name, _ := tuple[0].(string); name == "total" {
			val, _ := tuple[2].(float64)
			return val
		}
	}

	return 0
}

// transformResources converts resource_statuses (map[name]status) to a PuppetDB resources array.
func transformResources(resourceStatuses any) []any {
	statuses, ok := resourceStatuses.(map[string]any)
	if !ok {
		return []any{}
	}

	resources := make([]any, 0, len(statuses))
	for _, rs := range statuses {
		rsMap, ok := rs.(map[string]any)
		if !ok {
			continue
		}

		resource := map[string]any{
			"skipped":           rsMap["skipped"],
			"timestamp":         rsMap["time"],
			"resource_type":     rsMap["resource_type"],
			"resource_title":    anyToString(rsMap["title"]),
			"file":              rsMap["file"],
			"line":              rsMap["line"],
			"containment_path":  rsMap["containment_path"],
			"corrective_change": rsMap["corrective_change"],
			"events":            transformEvents(rsMap["events"]),
		}

		resources = append(resources, resource)
	}

	return resources
}

// transformEvents converts events from to_data_hash format to PuppetDB format.
func transformEvents(events any) []any {
	eventList, ok := events.([]any)
	if !ok {
		return []any{}
	}

	result := make([]any, 0, len(eventList))
	for _, e := range eventList {
		eMap, ok := e.(map[string]any)
		if !ok {
			continue
		}

		event := map[string]any{
			"status":            eMap["status"],
			"timestamp":         eMap["time"],
			"name":              eMap["name"],
			"property":          eMap["property"],
			"new_value":         anyToString(eMap["desired_value"]),
			"old_value":         anyToString(eMap["previous_value"]),
			"corrective_change": eMap["corrective_change"],
			"message":           eMap["message"],
		}

		result = append(result, event)
	}

	return result
}

// transformMetrics converts metrics from to_data_hash format
// ({key: {name, label, values: [[name, label, value], ...]}})
// to PuppetDB flat array format ([{category, name, value}]).
func transformMetrics(metrics any) []any {
	metricsMap, ok := metrics.(map[string]any)
	if !ok {
		return []any{}
	}

	var result []any
	for _, metricData := range metricsMap {
		mMap, ok := metricData.(map[string]any)
		if !ok {
			continue
		}

		category, _ := mMap["name"].(string)
		values, ok := mMap["values"].([]any)
		if !ok {
			continue
		}

		for _, v := range values {
			tuple, ok := v.([]any)
			if !ok || len(tuple) < 3 {
				continue
			}

			name, _ := tuple[0].(string)
			result = append(result, map[string]any{
				"category": category,
				"name":     name,
				"value":    tuple[2],
			})
		}
	}

	return result
}

// anyToString converts a value to string, matching Ruby's .to_s behavior (nil → "").
func anyToString(v any) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%v", v)
}
