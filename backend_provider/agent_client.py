"""
DarkLine Agent Client
Replaces three_xui.py — communicates directly with darkline-agent on each VPN server.

Agent API:
  GET  /health              → server health
  GET  /metrics             → CPU/RAM/disk/network/clients
  GET  /xray/status         → xray process status
  POST /xray/reload         → reload xray config
  POST /xray/restart        → restart xray process
  GET  /xray/inbounds       → list inbounds
  GET  /clients             → list all clients
  POST /clients/add         → add client
  POST /clients/remove      → remove client
  POST /clients/update      → update client expiry/traffic
  POST /inbound/ensure      → create inbound if not exists
"""

import logging
from dataclasses import dataclass
from typing import Optional

import httpx

log = logging.getLogger(__name__)


class AgentError(RuntimeError):
    pass


@dataclass
class AgentMetrics:
    cpu_percent:    float
    ram_percent:    float
    ram_used_mb:    int
    ram_total_mb:   int
    disk_percent:   float
    disk_used_gb:   float
    disk_total_gb:  float
    net_rx_bytes:   int
    net_tx_bytes:   int
    uptime_seconds: int
    load_avg_1:     float
    clients_count:  int
    xray_running:   bool
    xray_uptime:    int


class AgentClient:
    """
    Async HTTP client for darkline-agent.
    One instance per VPN server.
    """

    def __init__(
        self,
        base_url: str,
        token: str,
        timeout: float = 15.0,
        verify_ssl: bool = True,
    ) -> None:
        self.base_url = base_url.rstrip("/")
        self.token = token
        self.timeout = timeout
        self.verify_ssl = verify_ssl

    def _headers(self) -> dict:
        return {"X-Agent-Token": self.token}

    async def _get(self, path: str) -> dict:
        async with httpx.AsyncClient(timeout=self.timeout, verify=self.verify_ssl) as client:
            resp = await client.get(
                f"{self.base_url}{path}",
                headers=self._headers(),
            )
            resp.raise_for_status()
            return resp.json()

    async def _post(self, path: str, body: dict = None) -> dict:
        async with httpx.AsyncClient(timeout=self.timeout, verify=self.verify_ssl) as client:
            resp = await client.post(
                f"{self.base_url}{path}",
                json=body or {},
                headers=self._headers(),
            )
            resp.raise_for_status()
            return resp.json()

    # ── Health ────────────────────────────────────────────────────────────────

    async def health(self) -> dict:
        try:
            return await self._get("/health")
        except Exception as e:
            raise AgentError(f"health check failed: {e}") from e

    async def is_alive(self) -> bool:
        try:
            await self.health()
            return True
        except Exception:
            return False

    # ── Metrics ───────────────────────────────────────────────────────────────

    async def metrics(self) -> AgentMetrics:
        try:
            data = await self._get("/metrics")
            return AgentMetrics(
                cpu_percent=data.get("cpu_percent", 0),
                ram_percent=data.get("ram_percent", 0),
                ram_used_mb=data.get("ram_used_mb", 0),
                ram_total_mb=data.get("ram_total_mb", 0),
                disk_percent=data.get("disk_percent", 0),
                disk_used_gb=data.get("disk_used_gb", 0),
                disk_total_gb=data.get("disk_total_gb", 0),
                net_rx_bytes=data.get("net_rx_bytes", 0),
                net_tx_bytes=data.get("net_tx_bytes", 0),
                uptime_seconds=data.get("uptime_seconds", 0),
                load_avg_1=data.get("load_avg_1", 0),
                clients_count=data.get("clients_count", 0),
                xray_running=data.get("xray_running", False),
                xray_uptime=data.get("xray_uptime", 0),
            )
        except Exception as e:
            raise AgentError(f"metrics failed: {e}") from e

    # ── Xray control ──────────────────────────────────────────────────────────

    async def xray_status(self) -> dict:
        return await self._get("/xray/status")

    async def xray_reload(self) -> None:
        await self._post("/xray/reload")

    async def xray_restart(self) -> None:
        await self._post("/xray/restart")

    # ── Inbounds ──────────────────────────────────────────────────────────────

    async def list_inbounds(self) -> list:
        return await self._get("/xray/inbounds")

    async def ensure_inbound(
        self,
        tag: str,
        port: int,
        private_key: str,
        short_ids: list[str],
        server_names: list[str],
        dest: str = "www.nvidia.com:443",
    ) -> None:
        await self._post("/inbound/ensure", {
            "tag":          tag,
            "port":         port,
            "private_key":  private_key,
            "short_ids":    short_ids,
            "server_names": server_names,
            "dest":         dest,
        })

    # ── Clients ───────────────────────────────────────────────────────────────

    async def list_clients(self) -> dict:
        """Returns {inbound_tag: [clients]}"""
        return await self._get("/clients")

    async def add_client(
        self,
        client_id: str,
        email: str,
        inbound_tag: str = "darkline-reality",
        flow: str = "xtls-rprx-vision",
        total_gb: int = 0,
        expiry_ms: int = 0,
    ) -> None:
        try:
            await self._post("/clients/add", {
                "inbound_tag": inbound_tag,
                "id":          client_id,
                "email":       email,
                "flow":        flow,
                "total_gb":    total_gb,
                "expiry_ms":   expiry_ms,
            })
            log.info("Agent: added client %s to %s inbound=%s", client_id, self.base_url, inbound_tag)
        except Exception as e:
            raise AgentError(f"add_client failed: {e}") from e

    async def remove_client(
        self,
        client_id: str,
        inbound_tag: str = "",
    ) -> None:
        try:
            await self._post("/clients/remove", {
                "inbound_tag": inbound_tag,
                "id":          client_id,
            })
            log.info("Agent: removed client %s from %s", client_id, self.base_url)
        except Exception as e:
            raise AgentError(f"remove_client failed: {e}") from e

    async def update_client(
        self,
        client_id: str,
        expiry_ms: int = 0,
        total_gb: int = 0,
        inbound_tag: str = "",
    ) -> None:
        try:
            await self._post("/clients/update", {
                "inbound_tag": inbound_tag,
                "id":          client_id,
                "expiry_ms":   expiry_ms,
                "total_gb":    total_gb,
            })
        except Exception as e:
            raise AgentError(f"update_client failed: {e}") from e
