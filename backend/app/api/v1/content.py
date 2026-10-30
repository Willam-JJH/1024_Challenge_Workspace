"""公告与赞助商（公开读取）。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(tags=["content"])

register_stubs(
    router,
    [
        ("GET", "/announcements", "已发布的公告（横幅与新闻流）"),
        ("GET", "/sponsors", "赞助商条"),
        ("POST", "/csp-reports", "浏览器上报 CSP 违规（只记录）"),
        ("POST", "/link-previews", "生成链接卡片预览"),
        ("GET", "/github/repos/{owner}/{repo}", "GitHub 仓库信息卡（带缓存）"),
    ],
    tag="content",
)
