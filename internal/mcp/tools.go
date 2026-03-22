package mcp

import (
	"github.com/mark3labs/mcp-go/mcp"
)

func saveTool() mcp.Tool {
	return mcp.NewTool("save",
		mcp.WithDescription("Save an observation to memory. Inserts into Buffer layer with FTS5 indexing. Supports topic_key upsert (same topic_key = update in place)."),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Short, searchable title (e.g. 'Fixed N+1 in UserList')"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Structured content. Recommended format: What/Why/Where/Learned"),
		),
		mcp.WithString("observation_type",
			mcp.Description("Type of observation"),
			mcp.Enum("decision", "bugfix", "discovery", "pattern", "gotcha", "config", "preference", "question"),
		),
		mcp.WithString("kind",
			mcp.Description("Memory kind"),
			mcp.Enum("episodic", "semantic", "procedural"),
		),
		mcp.WithNumber("confidence",
			mcp.Description("Confidence level (0.0-1.0, default 0.7)"),
		),
		mcp.WithString("topic_key",
			mcp.Description("Unique key for upsert. Same topic_key in same namespace = update instead of duplicate."),
		),
		mcp.WithString("tags",
			mcp.Description("Comma-separated tags for classification and search"),
		),
		mcp.WithString("files",
			mcp.Description("Comma-separated file paths linked to this observation (relative to repo root)"),
		),
		mcp.WithString("namespace",
			mcp.Description("Namespace for isolation (default: 'default'). Use one per project."),
		),
		mcp.WithString("retention",
			mcp.Description("Memory retention policy: 'durable' (default, eligible for Core) or 'operational' (stays in fast memory, never promoted to Core). Auto-classified if omitted."),
			mcp.Enum("operational", "durable"),
		),
	)
}

func recallTool() mcp.Tool {
	return mcp.NewTool("recall",
		mcp.WithDescription("Search memories using FTS5 keyword search with tri-factor scoring (recency x importance x relevance). Returns ranked results. Automatically detects temporal intent in queries (current, history, when, duration) and adjusts ranking accordingly. Temporal keywords are stripped from FTS to improve recall."),
		mcp.WithString("query",
			mcp.Required(),
			mcp.Description("Search query (keywords)"),
		),
		mcp.WithString("observation_type",
			mcp.Description("Filter by observation type"),
			mcp.Enum("decision", "bugfix", "discovery", "pattern", "gotcha", "config", "preference", "question"),
		),
		mcp.WithString("kind",
			mcp.Description("Filter by memory kind"),
			mcp.Enum("episodic", "semantic", "procedural"),
		),
		mcp.WithString("namespace",
			mcp.Description("Filter by namespace"),
		),
		mcp.WithString("files",
			mcp.Description("Comma-separated file paths to filter by (returns observations linked to these files)"),
		),
		mcp.WithBoolean("include_stale",
			mcp.Description("Include stale/expired observations (default: false)"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results to return (default: 10, max: 50)"),
		),
	)
}

func contextTool() mcp.Tool {
	return mcp.NewTool("context",
		mcp.WithDescription("Get relevant context: recent + high-importance + file-linked observations. Use at session start to get proactive memory."),
		mcp.WithString("namespace",
			mcp.Description("Namespace to get context for"),
		),
		mcp.WithString("files",
			mcp.Description("Comma-separated file paths to get context for"),
		),
		mcp.WithNumber("limit",
			mcp.Description("Max results (default: 20)"),
		),
	)
}

func updateTool() mcp.Tool {
	return mcp.NewTool("update",
		mcp.WithDescription("Update an existing observation by ID."),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Observation ID to update"),
		),
		mcp.WithString("title",
			mcp.Required(),
			mcp.Description("Updated title"),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Updated content"),
		),
		mcp.WithString("observation_type",
			mcp.Description("Updated observation type"),
			mcp.Enum("decision", "bugfix", "discovery", "pattern", "gotcha", "config", "preference", "question"),
		),
		mcp.WithString("kind",
			mcp.Description("Updated memory kind"),
			mcp.Enum("episodic", "semantic", "procedural"),
		),
		mcp.WithNumber("confidence",
			mcp.Description("Updated confidence (0.0-1.0)"),
		),
		mcp.WithString("tags",
			mcp.Description("Updated comma-separated tags"),
		),
		mcp.WithString("files",
			mcp.Description("Updated comma-separated file paths"),
		),
		mcp.WithString("retention",
			mcp.Description("Memory retention policy: 'durable' (eligible for Core) or 'operational' (stays in fast memory). If omitted, the existing value is preserved."),
			mcp.Enum("operational", "durable"),
		),
	)
}

func forgetTool() mcp.Tool {
	return mcp.NewTool("forget",
		mcp.WithDescription("Soft-delete an observation. It won't appear in recall but remains in the database."),
		mcp.WithString("id",
			mcp.Required(),
			mcp.Description("Observation ID to forget"),
		),
	)
}

func invalidateTool() mcp.Tool {
	return mcp.NewTool("invalidate",
		mcp.WithDescription("Report an observation as incorrect. Marks it stale, halves confidence. Optionally provide replacement content which creates a new observation with a 'supersedes' link."),
		mcp.WithString("observation_id",
			mcp.Required(),
			mcp.Description("ID of the observation to invalidate"),
		),
		mcp.WithString("reason",
			mcp.Required(),
			mcp.Description("Why this observation is incorrect"),
		),
		mcp.WithString("replacement_title",
			mcp.Description("Title for the replacement observation (requires replacement_content too)"),
		),
		mcp.WithString("replacement_content",
			mcp.Description("Content for the replacement observation"),
		),
	)
}

func statusTool() mcp.Tool {
	return mcp.NewTool("status",
		mcp.WithDescription("Get brain statistics: layer counts, staleness stats, and health information."),
	)
}

func sessionStartTool() mcp.Tool {
	return mcp.NewTool("session_start",
		mcp.WithDescription("Start a new work session. Auto-closes previous active sessions. Returns session ID and relevant context."),
		mcp.WithString("title",
			mcp.Description("Session title"),
		),
		mcp.WithString("directory",
			mcp.Description("Working directory"),
		),
		mcp.WithString("branch",
			mcp.Description("Git branch"),
		),
		mcp.WithString("namespace",
			mcp.Description("Namespace for this session"),
		),
	)
}

func sessionEndTool() mcp.Tool {
	return mcp.NewTool("session_end",
		mcp.WithDescription("End a work session with a summary. Marks session as completed."),
		mcp.WithString("session_id",
			mcp.Required(),
			mcp.Description("Session ID to end"),
		),
		mcp.WithString("summary",
			mcp.Required(),
			mcp.Description("Session summary. Recommended: Goal/Discoveries/Accomplished/Next format."),
		),
	)
}

func gitHookTool() mcp.Tool {
	return mcp.NewTool("git_hook",
		mcp.WithDescription("Report changed files from a git commit. Marks linked observations as stale."),
		mcp.WithString("changed_files",
			mcp.Required(),
			mcp.Description("Comma-separated list of changed file paths"),
		),
		mcp.WithString("commit_sha",
			mcp.Required(),
			mcp.Description("Git commit SHA"),
		),
		mcp.WithString("branch",
			mcp.Description("Git branch name"),
		),
	)
}

func reflectTool() mcp.Tool {
	return mcp.NewTool("reflect",
		mcp.WithDescription("Trigger reflection: synthesize related observations into high-level insights. Requires LLM provider."),
		mcp.WithString("namespace",
			mcp.Description("Namespace to reflect on"),
		),
	)
}

func consolidateTool() mcp.Tool {
	return mcp.NewTool("consolidate",
		mcp.WithDescription("Force immediate memory consolidation: decay, promote, dedup, detect contradictions, reflect, and evict. Normally runs every 30 minutes automatically."),
	)
}

func healthCheckTool() mcp.Tool {
	return mcp.NewTool("health_check",
		mcp.WithDescription("Compute brain power score (0-100%) showing how much of Neurox's potential is being used. Returns per-dimension breakdown with status and actionable recommendations."),
		mcp.WithNumber("days",
			mcp.Description("Number of days to analyze for usage stats (default: 7)"),
		),
	)
}
