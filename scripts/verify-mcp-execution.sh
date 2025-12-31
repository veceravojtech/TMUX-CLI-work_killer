#!/bin/bash
# scripts/verify-mcp-execution.sh
# Verifies MCP server supports concurrent instances without conflicts

set -e  # Exit on error

echo "MCP Concurrent Server Verification"
echo "==================================="

# Build binary
echo "Building tmux-cli binary..."
go build -o ./tmux-cli ./cmd/tmux-cli
if [ $? -ne 0 ]; then
    echo "ERROR: Build failed"
    exit 1
fi

# Setup: Create 3 test project directories
echo ""
echo "Setup: Creating test project directories..."
TEST_DIRS=(
    "/tmp/mcp-test-project-1"
    "/tmp/mcp-test-project-2"
    "/tmp/mcp-test-project-3"
)

for dir in "${TEST_DIRS[@]}"; do
    rm -rf "$dir"
    mkdir -p "$dir"
    cat > "$dir/.tmux-cli-session.json" <<EOF
{
  "sessionID": "$(basename $dir)",
  "projectPath": "$dir",
  "windows": []
}
EOF
    echo "  Created: $dir"
done

# Start: Launch 3 MCP servers concurrently
echo ""
echo "Starting 3 concurrent MCP servers..."
PIDS=()

for i in "${!TEST_DIRS[@]}"; do
    dir="${TEST_DIRS[$i]}"
    (cd "$dir" && timeout 10 ../../../tmux-cli mcp > /dev/null 2>&1) &
    pid=$!
    PIDS+=($pid)
    echo "  Server $((i+1)): PID $pid (dir: $dir)"
    sleep 0.1  # Small delay to allow server startup
done

# Verify: Servers are running
echo ""
echo "Verifying all servers are running..."
for i in "${!PIDS[@]}"; do
    pid="${PIDS[$i]}"
    if ps -p $pid > /dev/null; then
        echo "  ✓ Server $((i+1)) (PID $pid) is running"
    else
        echo "  ✗ Server $((i+1)) (PID $pid) failed to start"
        exit 1
    fi
done

# Wait for stable operation
echo ""
echo "Waiting 2 seconds for stable operation..."
sleep 2

# Verify: No conflicts or crashes
echo ""
echo "Verifying servers still running (no conflicts)..."
for i in "${!PIDS[@]}"; do
    pid="${PIDS[$i]}"
    if ps -p $pid > /dev/null; then
        echo "  ✓ Server $((i+1)) (PID $pid) still running"
    else
        echo "  ✗ Server $((i+1)) (PID $pid) crashed"
        exit 1
    fi
done

# Cleanup: Shutdown all servers gracefully
echo ""
echo "Shutting down servers..."
for i in "${!PIDS[@]}"; do
    pid="${PIDS[$i]}"
    kill -SIGTERM $pid 2>/dev/null || true
    echo "  Sent SIGTERM to Server $((i+1)) (PID $pid)"
done

# Wait for graceful shutdown
sleep 1

# Verify: Clean shutdown (no orphaned processes)
echo ""
echo "Verifying clean shutdown..."
for i in "${!PIDS[@]}"; do
    pid="${PIDS[$i]}"
    if ps -p $pid > /dev/null 2>&1; then
        echo "  ✗ Server $((i+1)) (PID $pid) did not shutdown"
        kill -9 $pid 2>/dev/null || true
        exit 1
    else
        echo "  ✓ Server $((i+1)) (PID $pid) shutdown cleanly"
    fi
done

# Cleanup test directories
echo ""
echo "Cleaning up test directories..."
for dir in "${TEST_DIRS[@]}"; do
    rm -rf "$dir"
done

echo ""
echo "SUCCESS: All concurrent server tests passed!"
echo "  - 3 servers started independently"
echo "  - No file locking conflicts"
echo "  - Stable concurrent operation"
echo "  - Clean graceful shutdown"
exit 0
