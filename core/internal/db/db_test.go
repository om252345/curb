package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDB(t *testing.T) {
	// Create a temporary directory for test database
	tmpDir, err := os.MkdirTemp("", "curb-db-test")
	if err != nil {
		t.Fatalf("failed to create tmp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Check if tables were created
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table';")
	if err != nil {
		t.Fatalf("failed to query sqlite_master: %v", err)
	}
	defer rows.Close()

	tables := make(map[string]bool)
	for rows.Next() {
		var name string
		rows.Scan(&name)
		tables[name] = true
	}

	expectedTables := []string{"protected_resources", "endpoint_rules", "mcp_servers", "audit_logs"}
	for _, expected := range expectedTables {
		if !tables[expected] {
			t.Errorf("expected table %s not found", expected)
		}
	}

	// Check seeding
	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM endpoint_rules").Scan(&count)
	if err != nil {
		t.Errorf("failed to count rules: %v", err)
	}
	if count == 0 {
		t.Errorf("no rules seeded")
	}
}
