import uuid
import base64
import io
import logging
import secrets
from typing import Optional
from urllib.parse import urlencode, quote

import qrcode
from sqlalchemy import select
from sqlalchemy.ext.asyncio import AsyncSession

from app.core.config import settings
from app.core.exceptions import VpnServerError
from app.models.models import (
    Subscription, User, VpnAccount, VpnProtocol, VpnServer, VpnServerStatus,
)

log = logging.getLogger(__name__)

# Inbound tag used by default in all agent-managed servers
REALITY_TAG = "darkline-reality"


def _agent(server: VpnServer):
    """Create AgentClient for a server."""
    from app.providers.vpn.agent_client import AgentClient
    if not server.panel_api_url or not server.panel_api_token:
        raise VpnServerError(f"Server {server.name} has no agent_url/agent_token")
    return AgentClient(
        base_url=server.panel_api_url,
        token=server.panel_api_token,
    )


class VpnService:
    def __init__(self, db: AsyncSession):
        self.db = db

    # ── Server selection ──────────────────────────────────────────────────────

    async def available_servers(self) -> list[VpnServer]:
        return list((await self.db.scalars(
            select(VpnServer).where(
                VpnServer.is_active == True,
                VpnServer.status == VpnServerStatus.online,
                VpnServer.users_count < VpnServer.max_users,
            ).order_by(VpnServer.load_percent.asc(), VpnServer.users_count.asc())
        )).all())

    async def pick_server(self) -> VpnServer:
        servers = await self.available_servers()
        if not servers:
            raise VpnServerError("No available VPN servers")
        return servers[0]

    # ── Health check ──────────────────────────────────────────────────────────

    async def check_health(self, server: VpnServer) -> bool:
        try:
            client = _agent(server)
            data = await client.health()
            alive = data.get("ok", False) or data.get("xray", False)
            if alive:
                # update stats from health response
                m = await client.metrics()
                server.users_count = m.clients_count
                server.load_percent = int(m.cpu_percent)
                server.status = VpnServerStatus.online
            else:
                server.status = VpnServerStatus.offline
            await self.db.flush()
            return alive
        except Exception as e:
            log.warning("Agent healthcheck failed server=%s: %s", server.name, e)
            server.status = VpnServerStatus.offline
            await self.db.flush()
            return False

    async def sync_stats(self, server: VpnServer) -> None:
        await self.check_health(server)

    # ── Account creation ──────────────────────────────────────────────────────

    async def create_accounts_for_subscription(
        self, user: User, subscription: Subscription
    ) -> list[VpnAccount]:
        """Create VPN account on every available server."""
        existing = list((await self.db.scalars(
            select(VpnAccount).where(
                VpnAccount.subscription_id == subscription.id,
                VpnAccount.is_active == True,
            )
        )).all())

        existing_server_ids = {a.server_id for a in existing}
        servers = await self.available_servers()

        if not servers and not existing:
            raise VpnServerError("No available VPN servers")

        # Share same UUID across all servers for one subscription
        common_uuid = existing[0].uuid if existing else str(uuid.uuid4())
        master_token = existing[0].subscription_token if existing else uuid.uuid4().hex

        created: list[VpnAccount] = []
        for server in servers:
            if server.id in existing_server_ids:
                continue
            try:
                acc = await self._create_on_server(
                    user=user,
                    subscription=subscription,
                    server=server,
                    client_uuid=common_uuid,
                    subscription_token=master_token if not existing and not created else uuid.uuid4().hex,
                )
                created.append(acc)
            except Exception as e:
                log.error("VPN account creation failed server=%s: %s", server.name, e)

        if not existing and not created:
            raise VpnServerError("VPN account creation failed on all servers")

        return existing + created

    async def create_account(
        self,
        user: User,
        subscription: Subscription,
        server: Optional[VpnServer] = None,
    ) -> VpnAccount:
        if server is None:
            server = await self.pick_server()
        return await self._create_on_server(
            user=user,
            subscription=subscription,
            server=server,
            client_uuid=str(uuid.uuid4()),
            subscription_token=uuid.uuid4().hex,
        )

    async def _create_on_server(
        self,
        user: User,
        subscription: Subscription,
        server: VpnServer,
        client_uuid: str,
        subscription_token: str,
    ) -> VpnAccount:
        email = self._client_email(user, subscription)
        expire_ms = int(subscription.expires_at.timestamp() * 1000) if subscription.expires_at else 0
        traffic_gb = subscription.traffic_limit_gb or 0

        # Call agent
        client = _agent(server)
        await client.add_client(
            client_id=client_uuid,
            email=email,
            inbound_tag=REALITY_TAG,
            flow="xtls-rprx-vision",
            total_gb=traffic_gb,
            expiry_ms=expire_ms,
        )

        account = VpnAccount(
            id=uuid.uuid4(),
            user_id=user.id,
            subscription_id=subscription.id,
            server_id=server.id,
            protocol=VpnProtocol.vless,
            uuid=client_uuid,
            email=email,
            config_json={
                "inbound_tag": REALITY_TAG,
                "server_host": server.host,
            },
            subscription_token=subscription_token,
            traffic_limit_bytes=self._gb_to_bytes(subscription.traffic_limit_gb),
            is_active=True,
        )
        self.db.add(account)
        server.users_count = (server.users_count or 0) + 1
        await self.db.flush()

        log.info("VPN account created user=%s server=%s", user.id, server.name)
        return account

    async def disable_account(self, account: VpnAccount) -> None:
        server = await self.db.get(VpnServer, account.server_id)
        if server:
            try:
                client = _agent(server)
                inbound_tag = (account.config_json or {}).get("inbound_tag", REALITY_TAG)
                await client.remove_client(account.uuid, inbound_tag=inbound_tag)
                server.users_count = max(0, (server.users_count or 1) - 1)
            except Exception as e:
                log.error("Agent disable client failed: %s", e)
        account.is_active = False
        await self.db.flush()

    # ── Subscription link generation ──────────────────────────────────────────

    def sub_url(self, token: str) -> str:
        return f"{settings.subscription_base_url}/api/v1/sub/{token}"

    def vless_link(self, account: VpnAccount, server: VpnServer) -> str:
        pbk = server.reality_public_key or "YOUR_PUBLIC_KEY"
        sid = (server.reality_short_ids or ["YOUR_SHORT_ID"])[0]
        sni = (server.reality_server_names or [server.host])[0]
        fp  = server.reality_fingerprint or "chrome"

        params = {
            "type":       "tcp",
            "security":   "reality",
            "encryption": "none",
            "sni":        sni,
            "fp":         fp,
            "pbk":        pbk,
            "sid":        sid,
            "spx":        "/",
            "flow":       "xtls-rprx-vision",
        }
        name = f"{server.name} - DarkLine"
        return f"vless://{account.uuid}@{server.host}:443?{urlencode(params, safe='/')}#{quote(name)}"

    def subscription_content(self, accounts: list, servers: dict) -> str:
        links = []
        for acc in accounts:
            srv = servers.get(str(acc.server_id))
            if srv and acc.is_active:
                links.append(self.vless_link(acc, srv))
        return base64.b64encode("\n".join(links).encode()).decode()

    def clash_config(self, account: VpnAccount, server: VpnServer) -> str:
        pbk = server.reality_public_key or "YOUR_PUBLIC_KEY"
        sid = (server.reality_short_ids or ["YOUR_SHORT_ID"])[0]
        sni = (server.reality_server_names or [server.host])[0]
        return f"""proxies:
  - name: "{server.name} - DarkLine"
    type: vless
    server: {server.host}
    port: 443
    uuid: {account.uuid}
    network: tcp
    tls: true
    flow: xtls-rprx-vision
    servername: {sni}
    reality-opts:
      public-key: {pbk}
      short-id: {sid}
    client-fingerprint: {server.reality_fingerprint or 'chrome'}
proxy-groups:
  - name: DarkLine
    type: select
    proxies:
      - "{server.name} - DarkLine"
rules:
  - MATCH,DarkLine
"""

    def qr_bytes(self, content: str) -> bytes:
        qr = qrcode.QRCode(version=1, error_correction=qrcode.constants.ERROR_CORRECT_M, box_size=10, border=4)
        qr.add_data(content)
        qr.make(fit=True)
        img = qr.make_image(fill_color="#22c55e", back_color="#0a0a0a")
        buf = io.BytesIO()
        img.save(buf, format="PNG")
        return buf.getvalue()

    @staticmethod
    def _client_email(user: User, subscription: Subscription) -> str:
        base = user.telegram_id or (user.email or str(user.id))
        return f"dl-{str(base).replace('@','-').replace('.','-')[:24]}-{str(subscription.id)[:8]}"

    @staticmethod
    def _gb_to_bytes(gb: Optional[int]) -> int:
        return (gb * 1024 ** 3) if gb else 0
