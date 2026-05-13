#!/usr/bin/env bash
set -euo pipefail

BASE_URL="https://tmux.vojta.ai/releases"
BINARY="tmux-cli"
INSTALL_DIR="${INSTALL_DIR:-$HOME/.local/bin}"

die() { echo "Error: $1" >&2; exit 1; }

OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)  OS_TAG="linux" ;;
  Darwin) OS_TAG="darwin" ;;
  *)      die "Unsupported OS: $OS" ;;
esac

case "$ARCH" in
  x86_64|amd64)  ARCH_TAG="amd64" ;;
  aarch64|arm64) ARCH_TAG="arm64" ;;
  *)             die "Unsupported architecture: $ARCH" ;;
esac

ASSET="${BINARY}-${OS_TAG}-${ARCH_TAG}.tar.gz"
URL="${BASE_URL}/${ASSET}"

echo "Downloading ${BINARY} (${OS_TAG}/${ARCH_TAG})..."

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

curl -fSL "$URL" -o "$TMPDIR/$ASSET"
tar -xzf "$TMPDIR/$ASSET" -C "$TMPDIR"

mkdir -p "$INSTALL_DIR"
mv "$TMPDIR/$BINARY" "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"

echo "Installed ${BINARY} to ${INSTALL_DIR}/${BINARY}"

if ! echo "$PATH" | tr ':' '\n' | grep -qx "$INSTALL_DIR"; then
  echo ""
  echo "Add to your PATH:"
  echo "  export PATH=\"\$PATH:${INSTALL_DIR}\""
fi

CLAUDE_JSON="$HOME/.claude.json"
BINARY_PATH="${INSTALL_DIR}/${BINARY}"
if [ -f "$CLAUDE_JSON" ]; then
  if python3 -c "import json,sys; d=json.load(open(sys.argv[1])); sys.exit(0 if 'tmux-cli' in d.get('mcpServers',{}) else 1)" "$CLAUDE_JSON" 2>/dev/null; then
    echo "MCP server already registered in Claude Code."
  else
    python3 -c "
import json, sys
p = sys.argv[1]
d = json.load(open(p))
d.setdefault('mcpServers', {})['tmux-cli'] = {'type': 'stdio', 'command': sys.argv[2], 'args': ['mcp'], 'env': {}}
json.dump(d, open(p, 'w'), indent=2)
" "$CLAUDE_JSON" "$BINARY_PATH"
    echo "Registered tmux-cli MCP server in Claude Code."
  fi
else
  python3 -c "
import json, sys
json.dump({'mcpServers': {'tmux-cli': {'type': 'stdio', 'command': sys.argv[1], 'args': ['mcp'], 'env': {}}}}, open(sys.argv[2], 'w'), indent=2)
" "$BINARY_PATH" "$CLAUDE_JSON"
  echo "Created Claude Code config with tmux-cli MCP server."
fi
