"""客户端地址。

部署在 Cloudflare 之后时，真实访客地址在 ``CF-Connecting-IP``；只有来自
受信代理（``APP_TRUSTED_PROXIES``）的连接才采信该头，避免伪造。
"""

from ipaddress import ip_address, ip_network

from fastapi import Request

from app.core.config import settings

_NETWORKS = []
for item in settings.trusted_proxies:
    try:
        _NETWORKS.append(ip_network(item, strict=False))
    except ValueError:
        continue


def _peer_is_trusted(peer: str | None) -> bool:
    if not peer or not settings.behind_cloudflare:
        return False
    try:
        addr = ip_address(peer)
    except ValueError:
        return False
    return any(addr in net for net in _NETWORKS)


def client_ip(request: Request) -> str | None:
    peer = request.client.host if request.client else None
    if _peer_is_trusted(peer):
        forwarded = request.headers.get("cf-connecting-ip")
        if forwarded:
            return forwarded.strip()
    return peer
