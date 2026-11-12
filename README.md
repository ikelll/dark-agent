
Готовый linux-amd64 бинарник:
  darkline-agent-linux-amd64

Установка готового бинарника на VPN-сервер:
  sudo install -m 755 darkline-agent-linux-amd64 /usr/local/bin/darkline-agent
  sudo systemctl restart darkline-agent
  sudo systemctl status darkline-agent --no-pager


### uTLS fingerprints and relay failover

`POST /relay/outbound/setup` accepts `reality.fingerprint` (optional, defaults to
`chrome` for existing callers). The fingerprint belongs to the dialing outbound;
no fingerprint is required on a receiving REALITY inbound.

`POST /relay/fingerprints` accepts a batch:

```json
{"fingerprints":{"relay-out-example":"firefox"}}
```

The authenticated endpoint validates the entire batch, updates only existing
VLESS/REALITY outbounds and preserves unrelated JSON fields. A changed batch is
validated/reloaded once; an unchanged batch avoids a restart. Configuration
application uses the existing backup/rollback mechanism. Invalid or missing tags
reject the entire batch. A reload briefly interrupts existing connections.

Supported values: chrome, firefox, safari, ios, android, edge, 360, qq, random,
randomized. Client compatibility must also be checked in the consuming app;
changing subscription metadata does not update an already imported client profile
until the app refreshes it and reconnects.

`POST /relay/balancer/setup` now configures concurrent relay-path observations,
`leastPing` selection and an explicit blackhole fallback when all exits fail.
The probe endpoint is an external dependency. This selects observed healthy
outbounds; it does not migrate established TCP connections or measure server CPU
load. The current default probe is https://www.gstatic.com/generate_204 every 10s.
