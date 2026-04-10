# Curb for VS Code

Curb is a lightweight, out-of-band security mesh that acts as a zero-trust safety net for AI coding assistants like Claude Code, Cursor, Windsurf, and Copilot.

When you use AI agents in your VS Code terminal or integrated tools, they often run with your full system permissions. Curb wraps those environments, ensuring that agents can only execute commands and access files that you have explicitly allowed in your workspace policy (`.curb.yml`).

![Demo](https://raw.githubusercontent.com/om252345/curb/main/extensions/vscode/images/demo.gif)

## Features

- **Terminal Protection (PTY Guard)**: Automatically intercepts commands run in the integrated terminal. If an agent tries to run `git push --force` or `rm -rf`, Curb catches it before execution.
- **File Guard**: Protects sensitive files like `.env`, `*.pem`, or production configurations from being read or modified by agentic tools.
- **MCP Payload Inspection**: Proxies the Model Context Protocol (MCP). Instead of just turning a tool "on" or "off", Curb inspects the exact arguments. You can mathematically guarantee an agent cannot create a GitHub PR against the `main` branch, but allow it for `dev`.
- **Live Dashboard**: A built-in webview dashboard that shows a real-time audit log of every command and file access attempt by your AI assistant.
- **Human-In-The-Loop (HITL)**: Move at the speed of thought. Turn on 100% auto-approval for your agent, and let Curb pause execution *only* when a restricted action is triggered, asking for your explicit approval right within VS Code.

## Requirements

The VS Code extension bundles the ultra-fast Go-based `curb` daemon. It requires:
- macOS (Intel/Apple Silicon), Linux, or Windows.
- VS Code version 1.90.0 or higher.
- (Optional but recommended) `curl` or `wget` for standalone CLI agent usage outside of VS Code.

## Extension Settings

This extension contributes the following settings/commands:

* `curb.setupTerminalGuard`: Injects the Curb terminal protection into your active shell sessions.
* `curb.openDashboard`: Opens the real-time audit and compliance dashboard.
* `curb.unlockFile`: Temporarily unlocks a protected file for manual human editing (HITL escape hatch).
* `curb.selectMcpConfig`: Re-links your Cursor or Claude MCP configuration files to the Curb proxy.

## Workspace Configuration

Share your security policy with your team by adding a `.curb.yml` to your workspace root:

```yaml
version: 1
files:
  protect: ["*.env"]
cli:
  rules:
    - name: "Block Force Push"
      command: "git"
      condition: "args.contains('push') && args.contains('--force')"
      action: "block"
```

## Known Issues

- Remote SSH/Containers: Full terminal interception may require manual `.bashrc` or `.zshrc` updates in the remote environment.
- On Windows, the terminal guard relies on `.cmd` wrappers which might not perfectly intercept native PowerShell commands if invoked directly without `cmd.exe`.

## Release Notes

### 0.1.0

- Initial Alpha Release!
- Introduction of the Four Guards: PTY, File, Git, and MCP Mesh.
- Built-in Dashboards and CEL-based rule engine.

---

**Built with ❤️ by the CurbDev team.**
