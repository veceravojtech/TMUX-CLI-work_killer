#!/bin/bash
# verify-real-execution.sh
# End-to-end verification that tmux-cli state matches actual tmux reality
set -e

echo "=== Real Execution Verification ==="
echo ""

# Generate unique test ID
if command -v uuidgen &> /dev/null; then
    TEST_ID=$(uuidgen | tr '[:upper:]' '[:lower:]')
else
    # Fallback if uuidgen not available
    TEST_ID="test-$(date +%s)-$$"
fi

# Cleanup function
cleanup() {
    echo ""
    echo "5. Cleanup..."
    ./tmux-cli session end --id "$TEST_ID" 2>/dev/null || true
    tmux kill-session -t "$TEST_ID" 2>/dev/null || true
}
trap cleanup EXIT

echo "Test Session ID: $TEST_ID"
echo ""

# Step 1: Create session
echo "1. Creating session..."
if ! ./tmux-cli session start --id "$TEST_ID" --path /tmp; then
    echo "   ❌ FAILED: Could not create session"
    exit 1
fi
echo "   ✅ Session created via tmux-cli"

# Step 2: Verify session exists in real tmux
echo ""
echo "2. Verifying session exists in tmux..."
if tmux has-session -t "$TEST_ID" 2>/dev/null; then
    echo "   ✅ Session exists in tmux"
else
    echo "   ❌ FAILED: Session not found in tmux"
    echo "   tmux-cli created it, but tmux doesn't see it!"
    exit 1
fi

# Step 3: Create windows
echo ""
echo "3. Creating windows..."
./tmux-cli session --id "$TEST_ID" windows create --name w1 --command "sleep 60"
./tmux-cli session --id "$TEST_ID" windows create --name w2 --command "sleep 60"
echo "   ✅ Windows created via tmux-cli"

# Step 4: Cross-verify window IDs
echo ""
echo "4. Cross-verifying window IDs between tmux-cli and tmux..."

# Get window IDs from tmux-cli's JSON file
JSON_FILE="$HOME/.tmux-cli/sessions/$TEST_ID.json"
if [ ! -f "$JSON_FILE" ]; then
    echo "   ❌ FAILED: JSON file not found: $JSON_FILE"
    exit 1
fi

# Extract window IDs from JSON (using grep/sed if jq not available)
if command -v jq &> /dev/null; then
    WINDOW_IDS=$(jq -r '.windows[].tmuxWindowId' "$JSON_FILE")
else
    # Fallback without jq
    WINDOW_IDS=$(grep -o '"tmuxWindowId":"[^"]*"' "$JSON_FILE" | cut -d'"' -f4)
fi

echo "   Window IDs in JSON:"
echo "$WINDOW_IDS" | sed 's/^/     - /'

# Get window IDs from actual tmux
TMUX_WINDOW_IDS=$(tmux list-windows -t "$TEST_ID" -F "#{window_id}")
echo ""
echo "   Window IDs in tmux:"
echo "$TMUX_WINDOW_IDS" | sed 's/^/     - /'

# Verify each JSON window exists in tmux
echo ""
FAILED=0
for WIN_ID in $WINDOW_IDS; do
    if echo "$TMUX_WINDOW_IDS" | grep -q "^$WIN_ID$"; then
        echo "   ✅ Window $WIN_ID verified in tmux"
    else
        echo "   ❌ FAILED: Window $WIN_ID in JSON but NOT in tmux!"
        FAILED=1
    fi
done

if [ $FAILED -eq 1 ]; then
    echo ""
    echo "❌ Verification FAILED: tmux-cli state doesn't match tmux reality"
    exit 1
fi

# Verify window count
EXPECTED_COUNT=$(echo "$WINDOW_IDS" | wc -l)
# Note: tmux creates a default window (window 0), so we might have more
ACTUAL_COUNT=$(echo "$TMUX_WINDOW_IDS" | wc -l)

echo ""
echo "   Window count: $ACTUAL_COUNT in tmux (expected at least $EXPECTED_COUNT)"

if [ "$ACTUAL_COUNT" -ge "$EXPECTED_COUNT" ]; then
    echo "   ✅ Window count OK"
else
    echo "   ❌ FAILED: Expected at least $EXPECTED_COUNT windows, found $ACTUAL_COUNT"
    exit 1
fi

echo ""
echo "✅ ✅ ✅ Real execution verification PASSED ✅ ✅ ✅"
echo ""
echo "Verified:"
echo "  • Session created in both tmux-cli and tmux"
echo "  • Windows created in both tmux-cli and tmux"
echo "  • Window IDs in JSON match window IDs in tmux"
echo "  • State is consistent between tmux-cli and tmux"
