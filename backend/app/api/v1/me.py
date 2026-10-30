"""「我」相关的资源：资料、参赛身份、邀请、通知、钱包、API key。"""

from fastapi import APIRouter, Depends
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.stubs import register_stubs
from app.schemas.auth import ProfileUpdateIn
from app.schemas.common import UserOut

router = APIRouter(prefix="/me", tags=["me"])


@router.get("", response_model=UserOut, summary="我的账号")
def get_me(principal: Principal = Depends(require_auth)) -> UserOut:
    return UserOut.model_validate(principal.user)


@router.patch("", response_model=UserOut, summary="更新我的资料")
def update_me(
    payload: ProfileUpdateIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> UserOut:
    user = principal.user
    if payload.display_name is not None:
        user.display_name = payload.display_name
    if payload.locale is not None:
        user.locale = payload.locale
    db.add(user)
    db.flush()
    return UserOut.model_validate(user)


register_stubs(
    router,
    [
        ("GET", "/membership", "我在当前赛事的公司关系"),
        ("PUT", "/participation", "切换旁观／参赛，或报名当前赛事"),
        ("GET", "/invitations", "发给我的邀请"),
        ("POST", "/invitations/{invite_id}/acceptance", "接受邀请"),
        ("POST", "/invitations/{invite_id}/decline", "拒绝邀请"),
        ("GET", "/join-requests", "我发出的入队申请"),
        ("DELETE", "/join-requests/{request_id}", "撤回入队申请"),
        ("GET", "/notifications", "我的通知（最新在前）"),
        ("POST", "/notifications/read", "把通知标记为已读"),
        ("GET", "/digest-preferences", "邮件摘要偏好"),
        ("PUT", "/digest-preferences", "开关每日评论摘要"),
        ("GET", "/passport", "我的参观护照：印章与完成度"),
        ("GET", "/wallet", "我的硬币：已投公司与余额"),
        ("PUT", "/wallet", "整张选票一次性保存（最多三家公司）"),
        ("PUT", "/wallet/coins/{company_id}", "投一枚硬币（幂等）"),
        ("DELETE", "/wallet/coins/{company_id}", "撤回投给某公司的硬币（幂等）"),
        ("GET", "/api-keys", "我的 API key 列表"),
        ("POST", "/api-keys", "创建 API key（完整密钥只返回一次）"),
        ("DELETE", "/api-keys/{key_id}", "吊销 API key"),
        ("POST", "/api-keys/{key_id}/rotations", "轮换 API key"),
        ("GET", "/connected-apps", "我用账号登录过的应用与设备"),
        ("DELETE", "/connected-apps/{client_id}", "吊销某个应用的全部设备"),
        ("DELETE", "/connected-apps/{client_id}/devices/{device_id}", "登出某个应用的单个设备"),
        ("POST", "/deletion-request", "申请注销账号"),
        ("DELETE", "/deletion-request", "撤销注销申请"),
        ("GET", "/exhibition-assignment", "我在展览中负责介绍的公司"),
        ("GET", "/projects", "我公司的作品草稿"),
        ("GET", "/usage/summary", "我的 LLM 用量汇总"),
        ("GET", "/usage/requests", "我最近的 LLM 请求（仅数字）"),
    ],
    tag="me",
    auth="authenticated",
)
