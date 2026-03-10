package dynamic

// Package dynamic contains tool implementations generated or refined during
// autonomous self-evolution runs (Phase 100).
//
// These tools are registered at runtime via the tools.Registry.RegisterAndEnable
// mechanism, allowing the harness to expand its capabilities without
// static re-compilation of the core.
//
// Current Dynamic Tools:
// - SQLSearchTool: Execute SQL queries against local SQLite databases.
// - GraphvizTool: Render DOT graph definitions into images (PNG/SVG).
