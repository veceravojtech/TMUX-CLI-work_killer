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

## Releasing

Releases are one command. From a clean `main` that's in sync with origin:

```bash
./scripts/release.sh            # patch bump, e.g. 0.1.1 -> 0.1.2
./scripts/release.sh --minor    # 0.1.x -> 0.2.0
./scripts/release.sh --major    # 0.x.y -> 1.0.0
./scripts/release.sh --dry-run  # show the next version, change nothing
./scripts/release.sh --watch     # stream the CI run after pushing
```

The script computes the next version from the latest `vX.Y.Z` git tag, runs
the test gate, then creates and pushes the tag. The push triggers the
[release workflow](.github/workflows/release.yml), which:

1. builds binaries for linux/darwin × amd64/arm64 with the version baked in
   (`tmux-cli --version` reports the tag),
2. uploads them and `install.sh` to the server behind `https://tmux.vojta.ai`,
3. publishes a GitHub release with generated notes.

The install one-liner always fetches the latest published binaries.

**Deploy credentials:** the upload step authenticates with a dedicated SSH key
stored as the `DEPLOY_SSH_KEY` repository secret. To rotate it, generate a new
ed25519 key, add its public half to `deploy@`'s `authorized_keys` on the
server, and update the secret with `gh secret set DEPLOY_SSH_KEY`.

## Support

[Buy me a coffee](https://buymeacoffee.com/veceradev)

## License

MIT
