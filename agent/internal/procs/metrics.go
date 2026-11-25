package procs

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// metricsParser implements parsing key-value metrics out of an HTTP GET response.
// Priority: 1) a JSON map[string]number, 2) line-by-line whitespace splitting taking the trailing number of the first two fields.
type metricsParser struct {
	mu sync.Mutex
}

// newMetricsParser creates a new metrics parser.
func newMetricsParser() *metricsParser {
	return &metricsParser{}
}

// Parse reads the response body from r and returns the key-value metrics map.
// return format: map[key]value (value is a uint64)
func (p *metricsParser) Parse(r io.Reader) (map[string]uint64, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// try to parse it as a JSON map[string]number
	var jsonMap map[string]float64
	if err := json.Unmarshal(body, &jsonMap); err == nil {
		result := make(map[string]uint64, len(jsonMap))
		for k, v := range jsonMap {
			result[k] = uint64(v)
		}
		return result, nil
	}

	// fallback: split line by line on whitespace and take the trailing number of the first two fields
	// e.g. "rx 1234" or "received 5678 bytes"
	result := make(map[string]uint64)
	lines := strings.Split(strings.TrimSpace(string(body)), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			var val uint64
			n, err := fmt.Sscanf(fields[1], "%d", &val)
			if n == 1 && err == nil {
				result[fields[0]] = val
			}
		}
	}
	if len(result) > 0 {
		return result, nil
	}
	// compatibility: treat the whole line as the key and take 0 as the value or skip it
	return result, nil
}
