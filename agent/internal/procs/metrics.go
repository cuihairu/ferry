package procs

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"sync"
)

// metricsParser 是从 HTTP GET 响应中解析 key-value 指标的实现。
// 优先顺序：1) JSON map[string]number，2) 逐行 whitespace 分割，取前两字段的末数值。
type metricsParser struct {
	mu sync.Mutex
}

// newMetricsParser 创建一个新的指标解析器。
func newMetricsParser() *metricsParser {
	return &metricsParser{}
}

// Parse 从 r 中读取响应体并返回 key-value 指标映射。
// 返回格式：map[key]value（value 为 uint64）
func (p *metricsParser) Parse(r io.Reader) (map[string]uint64, error) {
	body, err := io.ReadAll(r)
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}

	// 尝试解析为 JSON map[string]number
	var jsonMap map[string]float64
	if err := json.Unmarshal(body, &jsonMap); err == nil {
		result := make(map[string]uint64, len(jsonMap))
		for k, v := range jsonMap {
			result[k] = uint64(v)
		}
		return result, nil
	}

	// 退回：逐行 whitespace 分割，取前两字段的末数值
	// 例如 "rx 1234" 或 "received 5678 bytes"
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
	// 兼容：把整行当 key，数值取 0 或跳过
	return result, nil
}
