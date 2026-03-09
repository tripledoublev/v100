package dynamic

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"github.com/tripledoublev/v100/internal/tools"
)

// SQLSearchTool provides SQL query capabilities for structured data access.
type SQLSearchTool struct{}

// NewSQLSearchTool creates a new SQL search tool instance.
func NewSQLSearchTool() tools.Tool {
	return &SQLSearchTool{}
}

func (t *SQLSearchTool) Name() string {
	return "sql_search"
}

func (t *SQLSearchTool) Description() string {
	return "Execute SQL queries against local SQLite databases."
}

func (t *SQLSearchTool) InputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"required": ["database_path", "query"],
		"properties": {
			"database_path": {"type": "string"},
			"query": {"type": "string"}
		}
	}`)
}

func (t *SQLSearchTool) OutputSchema() json.RawMessage {
	return json.RawMessage(`{
		"type": "object",
		"properties": {
			"rows": {"type": "array"},
			"columns": {"type": "array"},
			"row_count": {"type": "integer"}
		}
	}`)
}

func (t *SQLSearchTool) DangerLevel() tools.DangerLevel {
	return tools.Dangerous
}

func (t *SQLSearchTool) Effects() tools.ToolEffects {
	return tools.ToolEffects{
		MutatesWorkspace:   false,
		ExternalSideEffect: true,
	}
}

func (t *SQLSearchTool) Exec(ctx context.Context, call tools.ToolCallContext, args json.RawMessage) (tools.ToolResult, error) {
	start := time.Now()
	var a struct {
		DatabasePath string `json:"database_path"`
		Query        string `json:"query"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return tools.ToolResult{OK: false, Output: "invalid args: " + err.Error(), DurationMS: time.Since(start).Milliseconds()}, nil
	}

	dbPath, ok := call.Mapper.SecurePath(a.DatabasePath)
	if !ok {
		return tools.ToolResult{OK: false, Output: "illegal path", DurationMS: time.Since(start).Milliseconds()}, nil
	}

	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return tools.ToolResult{OK: false, Output: err.Error(), DurationMS: time.Since(start).Milliseconds()}, nil
	}
	defer db.Close()

	rows, err := db.QueryContext(ctx, a.Query)
	if err != nil {
		return tools.ToolResult{OK: false, Output: err.Error(), DurationMS: time.Since(start).Milliseconds()}, nil
	}
	defer rows.Close()

	columns, _ := rows.Columns()
	var results []map[string]interface{}
	for rows.Next() {
		values := make([]interface{}, len(columns))
		valuePtrs := make([]interface{}, len(columns))
		for i := range values {
			valuePtrs[i] = &values[i]
		}
		if err := rows.Scan(valuePtrs...); err != nil {
			return tools.ToolResult{OK: false, Output: err.Error(), DurationMS: time.Since(start).Milliseconds()}, nil
		}
		rowMap := make(map[string]interface{})
		for i, col := range columns {
			rowMap[col] = values[i]
		}
		results = append(results, rowMap)
	}

	out, _ := json.Marshal(map[string]interface{}{
		"rows":      results,
		"columns":   columns,
		"row_count": len(results),
	})

	return tools.ToolResult{
		OK:         true,
		Output:     string(out),
		DurationMS: time.Since(start).Milliseconds(),
	}, nil
}

var _ tools.Tool = (*SQLSearchTool)(nil)
