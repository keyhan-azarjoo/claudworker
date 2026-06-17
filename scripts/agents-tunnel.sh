#!/bin/bash
# Reverse tunnel: server 172.18.0.1:9787 -> this Mac's panel :8787 (for agents.example.com)
export SSHPASS=$(python3 -c "import json;print(json.load(open('$(cd "$(dirname "$0")/../.." && pwd)/Telegram/claude_telegram_bridge/.secrets.json'))['MYSERVER_PASSWORD'])")
while true; do
  sshpass -e ssh -o StrictHostKeyChecking=no -o ServerAliveInterval=30 -o ServerAliveCountMax=3 \
    -o ExitOnForwardFailure=yes -N -R 172.18.0.1:9787:localhost:8787 root@your-server.example.com
  echo "tunnel dropped — reconnecting in 5s"; sleep 5
done
