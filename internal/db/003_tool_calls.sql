CREATE TABLE IF NOT EXISTS tool_calls (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tool_name TEXT NOT NULL,
    namespace TEXT NOT NULL DEFAULT '',
    params_used TEXT NOT NULL DEFAULT '[]',
    success INTEGER NOT NULL DEFAULT 1,
    duration_ms INTEGER NOT NULL DEFAULT 0,
    called_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX IF NOT EXISTS idx_tc_tool ON tool_calls(tool_name);
CREATE INDEX IF NOT EXISTS idx_tc_called ON tool_calls(called_at);
