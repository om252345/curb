package state

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/om252345/curb/internal/config"
)

// EndpointRule maps directly to the v0.1 "Four Guards" blueprint schema
type EndpointRule struct {
	ID             int
	Name           string
	TriggerTargets string
	Condition      string
	Action         string
	ErrorMsg       string
}

type MCPServer struct {
	Name        string
	UpstreamCmd string
	EnvVars     string
	IsActive    bool
}

type PolicyCache struct {
	mu            sync.RWMutex
	EndpointRules []EndpointRule
	Servers       map[string]MCPServer
}

func NewPolicyCache() *PolicyCache {
	return &PolicyCache{
		Servers: make(map[string]MCPServer),
	}
}

func (c *PolicyCache) LoadFromDB(db *sql.DB) error {
	// Load endpoint rules (the Four Guards + AI drafts)
	rulesRows, err := db.Query("SELECT id, name, trigger_targets, condition, action, error_msg FROM endpoint_rules")
	if err != nil {
		return fmt.Errorf("query endpoint_rules: %w", err)
	}
	defer rulesRows.Close()

	var newRules []EndpointRule
	for rulesRows.Next() {
		var r EndpointRule
		if err := rulesRows.Scan(&r.ID, &r.Name, &r.TriggerTargets, &r.Condition, &r.Action, &r.ErrorMsg); err != nil {
			return err
		}
		newRules = append(newRules, r)
	}

	// Load MCP servers
	serverRows, err := db.Query("SELECT name, upstream_cmd, env_vars, is_active FROM mcp_servers WHERE is_active = 1")
	if err != nil {
		return fmt.Errorf("query mcp_servers: %w", err)
	}
	defer serverRows.Close()

	newServers := make(map[string]MCPServer)
	for serverRows.Next() {
		var s MCPServer
		if err := serverRows.Scan(&s.Name, &s.UpstreamCmd, &s.EnvVars, &s.IsActive); err != nil {
			return err
		}
		newServers[s.Name] = s
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	c.EndpointRules = newRules
	c.Servers = newServers

	return nil
}

func (c *PolicyCache) GetServer(serverID string) (MCPServer, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	s, ok := c.Servers[serverID]
	return s, ok
}

func (c *PolicyCache) GetAllServers() map[string]MCPServer {
	c.mu.RLock()
	defer c.mu.RUnlock()
	copyMap := make(map[string]MCPServer, len(c.Servers))
	for k, v := range c.Servers {
		copyMap[k] = v
	}
	return copyMap
}

func (c *PolicyCache) GetEndpointRules() []EndpointRule {
	c.mu.RLock()
	defer c.mu.RUnlock()
	rulesCopy := make([]EndpointRule, len(c.EndpointRules))
	copy(rulesCopy, c.EndpointRules)
	return rulesCopy
}

// ClearAndReload populates the cache from YAML CLI rules (replaces SQLite-based loading)
func (c *PolicyCache) ClearAndReload(cliRules []config.CLIRule) {
	c.mu.Lock()
	defer c.mu.Unlock()

	var newRules []EndpointRule
	for i, rule := range cliRules {
		newRules = append(newRules, EndpointRule{
			ID:             i,
			Name:           rule.Name,
			TriggerTargets: rule.Command,
			Condition:      rule.Condition,
			Action:         rule.Action,
			ErrorMsg:       fmt.Sprintf("Blocked by Curb: %s", rule.Name),
		})
	}
	c.EndpointRules = newRules
}

// LoadFromConfig populates both endpoint rules and MCP servers from YAML config
func (c *PolicyCache) LoadFromConfig(cfg *config.Config) {
	c.ClearAndReload(cfg.CLI.Rules)

	c.mu.Lock()
	defer c.mu.Unlock()

	newServers := make(map[string]MCPServer)
	for name, srv := range cfg.MCP.Servers {
		envBytes, _ := json.Marshal(srv.Env)
		newServers[name] = MCPServer{
			Name:        name,
			UpstreamCmd: srv.Upstream,
			EnvVars:     string(envBytes),
			IsActive:    true,
		}
	}
	c.Servers = newServers
}
