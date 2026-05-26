#!/bin/bash
# DarkLine Agent — installer
# Usage: curl -fsSL https://raw.../install.sh | bash -s -- --token YOUR_SECRET_TOKEN
# Or:    bash install.sh --token YOUR_SECRET_TOKEN --name NL-Amsterdam-01

set -e

AGENT_VERSION="latest"
AGENT_BIN="/usr/local/bin/darkline-agent"
AGENT_DIR="/etc/darkline-agent"
AGENT_CONFIG="$AGENT_DIR/config.json"
SERVICE_FILE="/etc/systemd/system/darkline-agent.service"
XRAY_BIN="/usr/local/bin/xray"
XRAY_CONFIG="/usr/local/etc/xray/config.json"
XRAY_DIR="/etc/xray"
LISTEN_ADDR=":7070"

# Parse args
TOKEN=""
SERVER_NAME=$(hostname)
while [[ $# -gt 0 ]]; do
  case $1 in
    --token)   TOKEN="$2";      shift 2 ;;
    --name)    SERVER_NAME="$2"; shift 2 ;;
    --port)    LISTEN_ADDR=":$2"; shift 2 ;;
    *) shift ;;
  esac
done

if [ -z "$TOKEN" ]; then
  TOKEN=$(openssl rand -hex 32)
  echo "Generated token: $TOKEN"
  echo ">>> Save this token! Add it to your backend .env as AGENT_TOKEN"
fi

# ── 1. Install Xray ─────────────────────────────────────────────────────────

echo "[1/5] Installing Xray..."
if [ ! -f "$XRAY_BIN" ]; then
  ARCH=$(uname -m)
  if [ "$ARCH" = "x86_64" ]; then XR_ARCH="64"
  elif [ "$ARCH" = "aarch64" ]; then XR_ARCH="arm64-v8a"
  else XR_ARCH="64"; fi

  mkdir -p /tmp/xray-install
  cd /tmp/xray-install
  curl -fsSLo xray.zip "https://github.com/XTLS/Xray-core/releases/latest/download/Xray-linux-${XR_ARCH}.zip"
  unzip -o xray.zip xray
  install -m 755 xray "$XRAY_BIN"
  cd /
  rm -rf /tmp/xray-install
  echo "Xray installed: $($XRAY_BIN version | head -1)"
else
  echo "Xray already at $XRAY_BIN"
fi

# ── 2. Create Xray config ────────────────────────────────────────────────────

echo "[2/5] Setting up Xray config..."
mkdir -p "$XRAY_DIR"

if [ ! -f "$XRAY_CONFIG" ]; then
  # Generate X25519 keys
  KEYS=$($XRAY_BIN x25519 2>/dev/null || echo "")
  PRIVATE_KEY=$(echo "$KEYS" | grep "Private key:" | awk '{print $3}')
  PUBLIC_KEY=$(echo "$KEYS"  | grep "Public key:"  | awk '{print $3}')

  if [ -z "$PRIVATE_KEY" ]; then
    # Fallback if x25519 subcommand not available
    PRIVATE_KEY="CHANGEME_private_key"
    PUBLIC_KEY="CHANGEME_public_key"
  fi

  SHORT_ID=$(openssl rand -hex 8)

  cat > "$XRAY_CONFIG" <<XRAYCFG
{
  "log": { "loglevel": "warning" },
  "inbounds": [
    {
      "tag": "darkline-reality",
      "port": 443,
      "protocol": "vless",
      "settings": {
        "clients": [],
        "decryption": "none"
      },
      "streamSettings": {
        "network": "tcp",
        "security": "reality",
        "realitySettings": {
          "show": false,
          "dest": "www.nvidia.com:443",
          "serverNames": ["www.nvidia.com"],
          "privateKey": "$PRIVATE_KEY",
          "shortIds": ["$SHORT_ID"]
        }
      },
      "sniffing": {
        "enabled": true,
        "destOverride": ["http", "tls", "quic"]
      }
    }
  ],
  "outbounds": [
    { "tag": "direct",  "protocol": "freedom"   },
    { "tag": "blocked", "protocol": "blackhole"  }
  ]
}
XRAYCFG
  echo "Xray config created. Public key: $PUBLIC_KEY"
  echo ">>> Add this to your server in admin panel: Reality Public Key = $PUBLIC_KEY"
else
  echo "Xray config already exists at $XRAY_CONFIG"
fi

# ── 3. Install agent binary ──────────────────────────────────────────────────

echo "[3/5] Installing darkline-agent..."
ARCH=$(uname -m)
if [ "$ARCH" = "x86_64" ];  then AGENT_ARCH="linux-amd64"
elif [ "$ARCH" = "aarch64" ]; then AGENT_ARCH="linux-arm64"
else AGENT_ARCH="linux-amd64"; fi

# Try download from GitHub releases, fall back to build instruction
if command -v curl &>/dev/null; then
  RELEASE_URL="https://github.com/darkerline/agent/releases/latest/download/darkline-agent-${AGENT_ARCH}"
  if curl --output /dev/null --silent --head --fail "$RELEASE_URL"; then
    curl -fsSLo "$AGENT_BIN" "$RELEASE_URL"
    chmod +x "$AGENT_BIN"
    echo "Agent downloaded from releases"
  else
    echo "No prebuilt binary found. Build manually:"
    echo "  go build -o darkline-agent ./cmd/agent && cp darkline-agent $AGENT_BIN"
  fi
fi

# ── 4. Write agent config ────────────────────────────────────────────────────

echo "[4/5] Writing agent config..."
mkdir -p "$AGENT_DIR"

cat > "$AGENT_CONFIG" <<CFG
{
  "listen_addr":   "$LISTEN_ADDR",
  "agent_token":   "$TOKEN",
  "xray_bin":      "$XRAY_BIN",
  "xray_config":   "$XRAY_CONFIG",
  "xray_api_addr": "127.0.0.1:10085",
  "server_name":   "$SERVER_NAME"
}
CFG

chmod 600 "$AGENT_CONFIG"
echo "Config: $AGENT_CONFIG"

# ── 5. Systemd service ───────────────────────────────────────────────────────

echo "[5/5] Creating systemd service..."
cat > "$SERVICE_FILE" <<SVC
[Unit]
Description=DarkLine Agent
After=network.target

[Service]
Type=simple
ExecStart=$AGENT_BIN -config $AGENT_CONFIG
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
SVC

systemctl daemon-reload
systemctl enable darkline-agent
systemctl restart darkline-agent

echo ""
echo "=========================================="
echo " DarkLine Agent installed!"
echo "=========================================="
echo " Server name:  $SERVER_NAME"
echo " Agent URL:    http://$(curl -s ifconfig.me 2>/dev/null || hostname -I | awk '{print $1}')${LISTEN_ADDR}"
echo " Agent token:  $TOKEN"
echo ""
echo " Status:  systemctl status darkline-agent"
echo " Logs:    journalctl -u darkline-agent -f"
echo "=========================================="
