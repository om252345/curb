# Curb: The Zero-Trust Safety Net for AI Agents

**Stop agents from destroying your workspace.**

Curb is a lightweight VS Code extension + high-performance Go backend that gives you real-time, preventive protection against destructive, sneaky, or unauthorized actions from Cursor, Claude Code, and VS Code agents. It works where it matters most — **in your real local workspace** — without forcing you into sandboxes or changing how you work.

> **Trust is the ultimate developer velocity.** When you can trust your agent to never `rm -rf` your project or leak your `.env` files, you can turn on 100% auto-approve and move at the speed of thought.
<p align="center">
  <img src="https://github.com/user-attachments/assets/892ac56e-c00e-47ba-91db-8efeea5eec4e" width="100%" style="max-width: 900px;"/>
  <br/>
  <sub>VS Code demo: For demonstration purposes, the agent is intentionally asked to execute potentially dangerous commands.</sub>
</p>

<p align="center">
  <img src="https://github.com/user-attachments/assets/09d1d776-aa7b-4e05-bd91-8556b4fd1886" width="100%" style="max-width: 900px;"/>
  <br/>
  <sub>Claude Code demo: For demonstration purposes, the agent is intentionally asked to execute potentially dangerous commands.</sub>
</p>

## Why Curb?

Modern AI agents have evolved from suggesting code to executing it. They now run terminal commands, edit files, push to git, and call MCP tools with full access to your actual workspace. Built-in agent sandboxes are self-managed by the AI, meaning a confused/hallucinating model or an over-ambitious agent can:

- **Delete files or entire folders** (e.g., accidental `rm -rf` or git history wipes)
- **Leak secrets** from `.env` or credentials
- **Force-push broken code** to main
- **Run sneaky workarounds** (Python scripts, base64 payloads, nested shells)

Built-in protections in Claude Code, Cursor, and Antigravity (Strict Mode, prompts, allow/deny lists) are helpful but still limited — they rely on the agent asking nicely or running inside a natively provided sandbox.

**Curb adds the missing last-mile preventive layer.** It intercepts every action at the OS level (PTY) and the Protocol level (MCP), ensuring that the agent only does what your workspace policy explicitly allows.

## Core Features

- **File Guard** — A kernel-inspired user-space firewall that blocks or requires approval for reading/writing sensitive paths (`.env`, secrets, credentials, etc.)
- **CLI Guard** — PTY-based terminal interception that catches raw commands, nested subshells, base64 payloads, and Python/Ruby workarounds.
- **Git Guard** — Specific Git operation monitoring that prevents dangerous workflows like force-pushes or unauthorized branch deletions.
- **MCP Guard** — A transparent mesh that deeply inspects Model Context Protocol tool calls using deterministic CEL (Common Expression Language) policies.

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
