package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// InitDB connects to the SQLite file using modernc.org/sqlite and creates tables if they do not exist.
func InitDB(dbPath string) (*sql.DB, error) {
	if info, err := os.Stat(dbPath); err == nil && info.IsDir() {
		dbPath = filepath.Join(dbPath, "curb.db")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Enable WAL to handle multiple mcp-proxy processes parsing db simultaneously 
	if _, err := db.Exec("PRAGMA journal_mode = WAL;"); err != nil {
		log.Printf("Warning: failed to enable WAL mode: %v", err)
	}
	db.Exec("PRAGMA synchronous = NORMAL;")
	db.Exec("PRAGMA busy_timeout = 5000;") // Wait 5000ms when locked

	// Enable foreign keys
	if _, err := db.Exec("PRAGMA foreign_keys = ON;"); err != nil {
		return nil, fmt.Errorf("failed to enable foreign keys: %w", err)
	}

	queries := []string{
		// 1. User-defined protected assets (Files only)
		`CREATE TABLE IF NOT EXISTS protected_resources (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			pattern TEXT NOT NULL
		);`,
		// 2. The Master Rules (CLI Commands only)
		`CREATE TABLE IF NOT EXISTS endpoint_rules (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			trigger_targets TEXT NOT NULL,
			condition TEXT NOT NULL,
			action TEXT NOT NULL,
			error_msg TEXT
		);`,
		// 3. MCP servers registry
		`CREATE TABLE IF NOT EXISTS mcp_servers (
			name TEXT PRIMARY KEY,
			upstream_cmd TEXT NOT NULL,
			env_vars TEXT,
			is_active BOOLEAN DEFAULT 1
		);`,
		// 4. MCP Tools Specific Policies
		`CREATE TABLE IF NOT EXISTS mcp_tool_policies (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			server_name TEXT NOT NULL,
			tool_name TEXT NOT NULL,
			condition TEXT NOT NULL,
			action TEXT NOT NULL,
			error_msg TEXT,
			FOREIGN KEY(server_name) REFERENCES mcp_servers(name) ON DELETE CASCADE
		);`,
		// 5. The Evidence
		`CREATE TABLE IF NOT EXISTS audit_logs (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			timestamp DATETIME DEFAULT CURRENT_TIMESTAMP,
			source TEXT NOT NULL,
			payload TEXT NOT NULL,
			action_taken TEXT NOT NULL
		);`,
	}

	for _, query := range queries {
		if _, err := db.Exec(query); err != nil {
			return nil, fmt.Errorf("failed to execute table creation query: %w\nQuery: %s", err, query)
		}
	}

	log.Println("Database initialized successfully with necessary schemas.")
	seedDefaults(db)
	return db, nil
}

func seedDefaults(db *sql.DB) {
	var count int
	db.QueryRow("SELECT COUNT(*) FROM endpoint_rules").Scan(&count)
	if count == 0 {
		db.Exec(`INSERT INTO endpoint_rules (name, trigger_targets, condition, action, error_msg) VALUES 
		('File Guard', 'cat,rm,mv,cp,vi,nano', 'args.exists(a, is_protected_file(a))', 'block', 'Blocked access to protected file.'),
		('Git Guard', 'git', '(args.contains("push") && args.contains("--force")) || (args.contains("reset") && args.contains("--hard"))', 'block', 'Destructive Git operation detected.'),
		('Cmd Guard', 'sh,bash,python3,node', 'depth > 1 && (payload.contains("os.system") || payload.contains("curl"))', 'block', 'Suspicious nested command detected.')`)

		db.Exec(`INSERT INTO protected_resources (pattern) VALUES ('.env')`)
		db.Exec(`INSERT INTO protected_resources (pattern) VALUES ('.env*')`)
	}
}
