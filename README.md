# Curb: The Zero-Trust Safety Net for AI Agents

Curb is a high-performance security mesh designed to bridge the trust gap between developers and AI coding assistants. By providing a transparent, policy-enforced sandbox, Curb allows you to fully leverage agentic AI without the fear of destructive or unauthorized actions.

> **Trust is the ultimate developer velocity.** When you can trust your agent to never `rm -rf` your project or leak your `.env` files, you can turn on 100% auto-approve and move at the speed of thought.

<!-- HERO SECTION: GIFs will be placed here -->
<p align="center">
  <br />
  <i>[Placeholder for GIF: Curb blocking a destructive command in VS Code]</i>
  &nbsp;&nbsp;&nbsp;
  <i>[Placeholder for GIF: Curb HITL pausing a destructive command]</i>
  <br />
</p>

## The Velocity Problem

Modern AI agents are incredibly powerful, but they operate with the same raw terminal permissions you do. Built-in agent sandboxes are self-managed by the AI, meaning a confused model can simply disable its own safety rails. 

Curb solves this. It sits entirely outside the agent's memory as a lightweight Go daemon. It wraps the OS-level PTY and proxies the MCP, ensuring the agent only executes what your workspace policy allows.

## The "Human-In-The-Loop" Advantage

Curb moves security from "babysitting" to "policy-in-the-mesh." You don't have to click "Approve" 50 times an hour. You let the agent run at full speed, and Curb only pauses execution for Human-In-The-Loop (HITL) approval when a restricted command or action is attempted.

## Why Curb?

Modern AI agents are incredibly powerful, but they operate with the same permissions as the developer. A single hallucination or an over-ambitious agent can result in:
- **Destructive File Operations**: Accidental deletion of source code or git history.
- **Data Exfiltration**: Reading sensitive configuration files or environment variables.
- **Unauthorized Network Access**: Sending local data to unknown upstream servers.
- **Broken Repositories**: Force-pushing half-baked code or dirtying the git state.

**Curb solves this by moving security from "human-in-the-loop" to "policy-in-the-mesh."** It intercepts every action at the OS level (PTY) and the Protocol level (MCP), ensuring that the agent only does what you've explicitly allowed.

## The Four Guards Architecture

Curb enforces security through four distinct layers of protection:

1.  **PTY Guard**: Intercepts shell commands at the terminal level. It can "peel" through nested shell wraps (e.g., `bash -c "sh -c '...'"` ) to see the true command being executed.
2.  **File Guard**: A kernel-level-inspired user-space firewall that prevents agents from reading or writing to protected patterns (e.g., `*.pem`, `id_rsa`, `.env`).
3.  **Git Guard**: Specifically monitors Git operations to prevent dangerous workflows like force-pushes or unauthorized branch deletions.
4.  **MCP Mesh**: A transparent proxy for the Model Context Protocol (MCP). It allows you to audit and block specific tool calls before they reach the upstream MCP server.

## Installation

### For CLI (Automation & Agents)
The quickest way to install the Curb binary and setup terminal protection:

```bash
curl -fsSL https://raw.githubusercontent.com/om252345/curb/main/install.sh | bash
```

### For VS Code
Install the **Curb Extension** from the VS Code Marketplace. The extension will automatically manage the background security engine and provide a real-time dashboard for rule violations.

## Usage

### 1. Configure Your Rules
Curb uses a simple, human-readable YAML configuration stored at `~/.curb/config.yml`.

```yaml
version: 1
files:
  protect: ["*.env", "**/auth_token.json"]
cli:
  rules:
    - name: "Block Force Push"
      command: "git"
      condition: "args.contains('push') && args.contains('--force')"
      action: "block"
mcp:
  servers:
    github:
      upstream: "npx @mcp/github"
      rules:
        # Curb doesn't just enable/disable tools, it interrogates the payload!
        - tool: "create_pull_request"
          condition: "args.base == 'main'"
          action: "hitl"
          error_msg: "Agent attempted to PR directly to main. Waiting for human approval."
```

By placing a `.curb.yml` (or `config.yml`) in your workspace, you can share these guardrails directly with your teammates. Ensure everyone on your team has the exact same safety net before letting their agents loose on your shared codebase.

### 2. Guarding CLI Agents (Claude Code, etc.)
Simply run your agent inside the Curb environment. To run **Claude Code** with Curb's full security mesh intercepting its commands:

```bash
curb run claude
```


### 3. VS Code Integration
Open the **Curb Dashboard** in VS Code to see a live audit log of every file access and command attempt your AI assistant makes. Approve or block requests in real-time with one click.

## Comparison: Curb vs Native Agent Guardrails

While agents like Claude Code or Antigravity offer their own internal guardrails or approval flows, **Curb operates out-of-band**.

| Feature | Curb (Out-of-Band) | Native Guardrails (Claude Code / Antigravity) |
| :--- | :--- | :--- |
| **Trust Model** | **Zero-Trust**: Enforced outside the agent's memory. | **High-Trust**: Enforced by the agent itself. |
| **Scope** | Global (intercepts *any* CLI or MCP agent). | Local (only protects that specific agent). |
| **Bypass Risk** | **None**: The agent cannot turn off Curb. | **High**: A hallucinating model can often disable its own guardrails via prompt injection. |
| **Rule Sharing** | Shared `.curb.yml` across teams. | Hardcoded or buried in agent-specific settings. |
| **MCP Payload Inspection** | **Deep Inspection**: Block specific arguments (e.g., block PRs to `main` but allow PRs to `dev`). | **Shallow**: Usually just toggles a tool "on" or "off" entirely. |

## Technical Details

Curb is built for ultra-low latency and zero observability overhead:
- **Core Engine**: Written in Go for memory safety and concurrency. 
- **Platform Native GUI**: HITL triggers invoke seamless `osascript`, `zenity`, or `PresentationFramework` popups so prompts don't collide with raw terminal agent UIs.
- **Rule Engine**: Evaluates policies using **Google's Common Expression Language (CEL)**, providing high-performance, type-safe evaluation.
- **Communication**: Uses a high-speed JSON-RPC over stdio bridge between the IDE and the core security engine.

## License

Curb is released under the [Apache 2.0 License](LICENSE).

## Contributing

Curb is built by and for developers who believe that AI should be both powerful and predictable. We welcome contributions to the core engine, new IDE extensions, and the community rule-set.

---
<p align="center">
  Built with ❤️ by the **CurbDev** team.
</p>
