"""公开内容与系统小接口：公告、赞助商、CSP 上报、链接预览、GitHub 卡片。"""

import ipaddress
import logging
import re
import socket
from urllib.parse import urlparse

import httpx
from fastapi import APIRouter, Depends, Response
from sqlalchemy import select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import ApiError, Forbidden
from app.models.admin import Announcement, Sponsor
from app.schemas.admin import AnnouncementOut, SponsorOut
from app.schemas.me import CspReportIn, GithubRepoOut, LinkPreviewIn, LinkPreviewOut
from app.services import companies as company_service

router = APIRouter(tags=["content"])
log = logging.getLogger("challenge-api.content")

TITLE_RE = re.compile(r"<title[^>]*>(.*?)</title>", re.IGNORECASE | re.DOTALL)
OG_TITLE_RE = re.compile(r'property=["\']og:title["\']\s+content=["\']([^"\']+)', re.IGNORECASE)
OG_DESC_RE = re.compile(
    r'property=["\']og:description["\']\s+content=["\']([^"\']+)', re.IGNORECASE
)
OG_IMAGE_RE = re.compile(r'property=["\']og:image["\']\s+content=["\']([^"\']+)', re.IGNORECASE)


@router.get("/announcements", response_model=list[AnnouncementOut], summary="已发布的公告")
def list_announcements(db: DbSession = Depends(get_db)) -> list[AnnouncementOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Announcement)
        .where(Announcement.event_id == event.id, Announcement.status == "published")
        .order_by(Announcement.published_at.desc())
    ).all()
    return [AnnouncementOut.model_validate(row) for row in rows]


@router.get("/sponsors", response_model=list[SponsorOut], summary="赞助商条")
def list_sponsors(db: DbSession = Depends(get_db)) -> list[SponsorOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Sponsor)
        .where(Sponsor.event_id == event.id)
        .order_by(Sponsor.position, Sponsor.created_at)
    ).all()
    return [SponsorOut.model_validate(row) for row in rows]


@router.post("/csp-reports", status_code=204, summary="浏览器上报 CSP 违规（只记录）")
def csp_report(payload: CspReportIn) -> Response:
    log.warning("CSP violation: %s", payload.report)
    return Response(status_code=204)


def _assert_public_host(url: str) -> None:
    parsed = urlparse(url)
    if parsed.scheme not in {"http", "https"} or not parsed.hostname:
        raise ApiError("只接受 http/https 链接", code="invalid_url", status_code=422)
    try:
        infos = socket.getaddrinfo(parsed.hostname, None)
    except OSError as exc:
        raise ApiError("域名解析失败", code="invalid_url", status_code=422) from exc
    for info in infos:
        address = ipaddress.ip_address(info[4][0])
        if address.is_private or address.is_loopback or address.is_link_local:
            raise Forbidden("不能预览内网地址")


@router.post("/link-previews", response_model=LinkPreviewOut, summary="生成链接卡片预览")
def link_preview(
    payload: LinkPreviewIn, _: Principal = Depends(require_auth)
) -> LinkPreviewOut:
    _assert_public_host(payload.url)
    try:
        with httpx.Client(timeout=8, follow_redirects=True) as client:
            response = client.get(payload.url, headers={"User-Agent": "1024-challenge/0.1"})
    except httpx.HTTPError as exc:
        raise ApiError("抓取失败", code="fetch_failed", status_code=502) from exc

    html = response.text[:200_000]
    title = OG_TITLE_RE.search(html)
    if title is None:
        title = TITLE_RE.search(html)
    desc = OG_DESC_RE.search(html)
    image = OG_IMAGE_RE.search(html)
    return LinkPreviewOut(
        url=str(response.url),
        title=(title.group(1).strip()[:200] if title else None),
        description=(desc.group(1).strip()[:300] if desc else None),
        image=(image.group(1).strip() if image else None),
    )


@router.get("/github/repos/{owner}/{repo}", response_model=GithubRepoOut, summary="GitHub 仓库卡片")
def github_repo(
    owner: str, repo: str, _: Principal = Depends(require_auth)
) -> GithubRepoOut:
    try:
        with httpx.Client(timeout=8) as client:
            response = client.get(
                f"https://api.github.com/repos/{owner}/{repo}",
                headers={
                    "Accept": "application/vnd.github+json",
                    "User-Agent": "1024-challenge/0.1",
                },
            )
    except httpx.HTTPError as exc:
        raise ApiError("GitHub 请求失败", code="fetch_failed", status_code=502) from exc
    if response.status_code == 404:
        raise ApiError("仓库不存在或不是公开仓库", code="not_found", status_code=404)
    if response.status_code >= 400:
        raise ApiError("GitHub 返回错误", code="fetch_failed", status_code=502)

    data = response.json()
    return GithubRepoOut(
        full_name=data.get("full_name", f"{owner}/{repo}"),
        description=data.get("description"),
        stars=int(data.get("stargazers_count") or 0),
        language=data.get("language"),
        url=data.get("html_url", f"https://github.com/{owner}/{repo}"),
    )
