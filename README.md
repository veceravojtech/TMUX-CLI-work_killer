# TMUX-CLI

Parallel AI agent orchestration for Claude Code via tmux.

## Install

```bash
curl -fsSL https://tmux.vojta.ai/install.sh | bash
```

This installs the binary, registers the MCP server in Claude Code, and enables tmux mouse support.

## Setup

Open any project directory and configure:

```bash
tmux-cli setting
```

Toggle options with arrow keys and Enter. Defaults work out of the box.

## Start a session

```bash
tmux-cli start-attach
```

This creates a tmux session for your project and attaches to it. Run `claude` inside.

## Commands

Once inside Claude Code, two slash commands are available:

**`/tmux:plan`** — plan first, then implement in parallel

```
/tmux:plan add OAuth2 support to the auth module
```

**`/tmux:supervisor`** — execute immediately in parallel, no planning

```
/tmux:supervisor reverse engineer the legacy billing module
```

Both spawn parallel worker windows that run independently and report back.

See [docs/advanced-usage.md](docs/advanced-usage.md) for settings reference and internals.

## Support

[Buy me a coffee](https://buymeacoffee.com/veceradev)

## License

MIT
