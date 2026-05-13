# TMUX-CLI

A Go CLI and MCP server for managing tmux sessions. Built for AI agent coordination — lets Claude Code spawn, manage, and communicate across parallel tmux windows.

## Install

```bash
curl -fsSL https://tmux.vojta.ai/install.sh | bash
```

### Manual install

**Linux:**

```bash
curl -fSL https://tmux.vojta.ai/releases/tmux-cli-linux-amd64.tar.gz | tar -xz
sudo mv tmux-cli /usr/local/bin/
```

**macOS:**

```bash
curl -fSL https://tmux.vojta.ai/releases/tmux-cli-darwin-arm64.tar.gz | tar -xz
mv tmux-cli ~/.local/bin/
```

## Quick start

```bash
tmux-cli start              # Create session for current project
tmux-cli windows-create     # Create a worker window
tmux-cli windows-send       # Send command to a window
tmux-cli windows-message    # Send formatted inter-window message
tmux-cli mcp                # Start MCP server (stdin/stdout)
tmux-cli kill               # Kill session
```

## Support

If you find this useful, you can [buy me a coffee](https://buymeacoffee.com/veceradev).

## License

MIT
