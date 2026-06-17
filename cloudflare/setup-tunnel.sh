#!/usr/bin/env bash
# Expose the local control panel (127.0.0.1:8787) via Cloudflare Tunnel.
#   ./setup-tunnel.sh quick                 # instant trycloudflare URL, no login/DNS
#   ./setup-tunnel.sh named panel.example.com  # permanent (needs the zone on Cloudflare)
set -euo pipefail
MODE="${1:-quick}"
HOST="${2:-panel.example.com}"
PORT="${PORT:-8787}"
TUNNEL="claudworker-agents"

if ! command -v cloudflared >/dev/null 2>&1; then
  echo "installing cloudflared…"; brew install cloudflared
fi

case "$MODE" in
  quick)
    echo "Starting a quick tunnel to http://localhost:$PORT (Ctrl-C to stop)…"
    echo "→ copy the https://<...>.trycloudflare.com URL it prints. Keep it private."
    exec cloudflared tunnel --url "http://localhost:$PORT"
    ;;
  named)
    # one-time browser login (saves a cert under ~/.cloudflared — NOT your password)
    [ -f "$HOME/.cloudflared/cert.pem" ] || cloudflared tunnel login
    cloudflared tunnel create "$TUNNEL" 2>/dev/null || true
    ID=$(cloudflared tunnel list 2>/dev/null | awk -v t="$TUNNEL" '$2==t{print $1}')
    CFG="$HOME/.cloudflared/$TUNNEL.yml"
    cat > "$CFG" <<YML
tunnel: $ID
credentials-file: $HOME/.cloudflared/$ID.json
ingress:
  - hostname: $HOST
    service: http://localhost:$PORT
  - service: http_status:404
YML
    echo "wrote $CFG"
    cloudflared tunnel route dns "$TUNNEL" "$HOST" || {
      echo "!! DNS route failed — is $HOST's zone on Cloudflare? (example.com is on GoDaddy)"; }
    echo "Install as a service:  sudo cloudflared --config '$CFG' service install"
    echo "Or run now:            cloudflared tunnel --config '$CFG' run $TUNNEL"
    echo "Then protect $HOST with Cloudflare Access (email = keyhanazarjoo@gmail.com)."
    ;;
  *) echo "usage: setup-tunnel.sh [quick | named <hostname>]"; exit 1;;
esac
