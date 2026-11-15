"""客户端地址。

反向代理链的取法（与 Go 实现的 ``clientIP`` 一致）：

- 默认取 TCP 对端地址；
- 只有对端属于 ``APP_TRUSTED_PROXIES`` 时才采信转发头，避免客户端伪造；
- 采信时优先 ``CF-Connecting-IP``（``APP_BEHIND_CLOUDFLARE``），
  否则从右往左看 ``X-Forwarded-For``，跳过受信代理后的第一个地址就是访客。
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


def _is_trusted(addr: str | None) -> bool:
    if not addr:
        return False
    try:
        parsed = ip_address(addr)
    except ValueError:
        return False
    return any(parsed in net for net in _NETWORKS)


def _forwarded_ip(request: Request) -> str | None:
    if settings.behind_cloudflare:
        forwarded = request.headers.get("cf-connecting-ip")
        if forwarded:
            return forwarded.strip()
    chain = request.headers.get("x-forwarded-for")
    if not chain:
        return None
    hops = [hop.strip() for hop in chain.split(",") if hop.strip()]
    for hop in reversed(hops):
        if not _is_trusted(hop):
            return hop
    return hops[0] if hops else None


def client_ip(request: Request) -> str | None:
    peer = request.client.host if request.client else None
    if _is_trusted(peer):
        return _forwarded_ip(request) or peer
    return peer
