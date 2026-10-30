"""队友集市与邀请链接。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(tags=["finder"])

register_stubs(
    router,
    [
        ("GET", "/finder/companies", "队友集市：还在招人的公司"),
        ("GET", "/finder/people", "队友集市：还在找队的个人"),
        ("POST", "/invitations/previews", "预览邀请链接（无需登录）"),
        ("POST", "/invitations/{invitation_id}/acceptance", "通过邀请链接加入公司"),
        ("POST", "/invitations/{invitation_id}/decline", "拒绝邮件邀请"),
        ("PUT", "/me/registration", "报名当前赛事（旁听／参赛／组建公司）"),
    ],
    tag="finder",
)
