#!/bin/bash
# setup-claude-hooks.sh
# Sets up Claude Code hooks for auto-snapshots
# This script should be run as the vscode user

set -e

HOOKS_DIR="${HOME}/.claude"
HOOKS_FILE="${HOOKS_DIR}/hooks.json"
SYSTEM_HOOKS="/etc/stockyard/claude-hooks.json"

mkdir -p "${HOOKS_DIR}"

# If system hooks exist, use them
if [ -f "${SYSTEM_HOOKS}" ]; then
    cp "${SYSTEM_HOOKS}" "${HOOKS_FILE}"
    echo "Installed Claude Code hooks from ${SYSTEM_HOOKS}"
else
    # Create default hooks
    cat > "${HOOKS_FILE}" << 'EOF'
{
  "hooks": {
    "PostToolUse": [{
      "command": "stockyard-snapshot \"$CLAUDE_TOOL_NAME\""
    }]
  }
}
EOF
    echo "Created default Claude Code hooks"
fi

# Ensure correct permissions
chmod 644 "${HOOKS_FILE}"

echo "Claude Code hooks installed at ${HOOKS_FILE}"
