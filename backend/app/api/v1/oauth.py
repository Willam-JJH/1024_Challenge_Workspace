"""OAuth 2.1 授权服务器（桌面端登录）与用量。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

oauth_router = APIRouter(prefix="/oauth", tags=["oauth"])

register_stubs(
    oauth_router,
    [
        ("GET", "/authorize", "授权端点（授权码 + PKCE S256）"),
        ("GET", "/consent", "授权确认页所需信息"),
        ("POST", "/consent", "允许或拒绝应用，返回浏览器跳转地址"),
        ("POST", "/token", "令牌端点：授权码换令牌、刷新令牌轮换"),
        ("POST", "/revoke", "令牌吊销（RFC 7009）"),
        ("GET", "/userinfo", "令牌归属（需 profile 作用域）"),
    ],
    tag="oauth",
)

usage_router = APIRouter(prefix="/usage", tags=["usage"])

register_stubs(
    usage_router,
    [
        ("GET", "/summary", "我的 LLM 用量汇总"),
        ("GET", "/requests", "我最近的 LLM 请求"),
        ("GET", "/team", "我公司的用量"),
        ("GET", "/teams", "各公司消耗排行（管理台）"),
        ("GET", "/users", "各人消耗排行（管理台）"),
    ],
    tag="usage",
    auth="authenticated",
)

well_known_router = APIRouter(tags=["oauth"])

register_stubs(
    well_known_router,
    [("GET", "/.well-known/oauth-authorization-server", "授权服务器元数据（RFC 8414）")],
    tag="oauth",
)
