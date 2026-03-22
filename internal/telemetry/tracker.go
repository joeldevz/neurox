package telemetry

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"time"
)

// Tracker records MCP tool call telemetry asynchronously.
type Tracker struct {
	db *sql.DB
}

// NewTracker creates a new Tracker backed by the given database.
func NewTracker(db *sql.DB) *Tracker {
	return &Tracker{db: db}
}

// CallRecord holds information about a single tool invocation.
type CallRecord struct {
	ToolName   string
	Namespace  string
	ParamsUsed []string // names of non-empty params
	Success    bool
	DurationMs int64
}

// Record logs a tool call asynchronously. Never blocks the caller.
func (t *Tracker) Record(record CallRecord) {
	go func() {
		paramsJSON, _ := json.Marshal(record.ParamsUsed)
		success := 0
		if record.Success {
			success = 1
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if _, err := t.db.ExecContext(ctx,
			`INSERT INTO tool_calls(tool_name, namespace, params_used, success, duration_ms)
			 VALUES(?, ?, ?, ?, ?)`,
			record.ToolName, record.Namespace, string(paramsJSON), success, record.DurationMs,
		); err != nil {
			log.Printf("telemetry record: %v", err)
		}
	}()
}

// ParamStats holds usage counts for a single tool.
type ParamStats struct {
	Total   int            `json:"total"`
	ByParam map[string]int `json:"by_param"`
}

// UsageReport is the aggregated telemetry report for a time window.
type UsageReport struct {
	TotalCalls       int                   `json:"total_calls"`
	CallsByTool      map[string]int        `json:"calls_by_tool"`
	ParamUsageByTool map[string]ParamStats `json:"param_usage_by_tool"`
	NeverUsed        []string              `json:"never_used"`
	Period           string                `json:"period"`
}

// AllTools is the canonical list of MCP tool names tracked.
var AllTools = []string{
	"save", "recall", "context", "update", "forget", "invalidate",
	"status", "session_start", "session_end", "git_hook", "reflect",
	"consolidate", "health_check",
}

// GetUsageReport returns aggregated tool-call telemetry for the given day window.
func (t *Tracker) GetUsageReport(ctx context.Context, days int) (UsageReport, error) {
	report := UsageReport{
		CallsByTool:      make(map[string]int),
		ParamUsageByTool: make(map[string]ParamStats),
		Period:           fmt.Sprintf("last %d days", days),
	}

	cutoff := time.Now().UTC().AddDate(0, 0, -days).Format("2006-01-02 15:04:05")

	rows, err := t.db.QueryContext(ctx,
		`SELECT tool_name, COUNT(*) FROM tool_calls
		 WHERE called_at >= ? GROUP BY tool_name ORDER BY COUNT(*) DESC`, cutoff)
	if err != nil {
		return report, fmt.Errorf("query tool calls: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name string
		var count int
		if err := rows.Scan(&name, &count); err != nil {
			continue
		}
		report.CallsByTool[name] = count
		report.TotalCalls += count
	}

	paramRows, err := t.db.QueryContext(ctx,
		`SELECT tool_name, params_used FROM tool_calls WHERE called_at >= ?`, cutoff)
	if err != nil {
		return report, fmt.Errorf("query param usage: %w", err)
	}
	defer paramRows.Close()

	for paramRows.Next() {
		var toolName, paramsJSON string
		if err := paramRows.Scan(&toolName, &paramsJSON); err != nil {
			continue
		}
		stats, ok := report.ParamUsageByTool[toolName]
		if !ok {
			stats = ParamStats{ByParam: make(map[string]int)}
		}
		stats.Total++
		var params []string
		if json.Unmarshal([]byte(paramsJSON), &params) == nil {
			for _, p := range params {
				stats.ByParam[p]++
			}
		}
		report.ParamUsageByTool[toolName] = stats
	}

	for _, tool := range AllTools {
		if _, ok := report.CallsByTool[tool]; !ok {
			report.NeverUsed = append(report.NeverUsed, tool)
		}
	}

	return report, nil
}
