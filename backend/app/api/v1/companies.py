"""公司（参赛队）：公开资料、组建、名单与邀请。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/companies", tags=["companies"])

register_stubs(
    router,
    [
        ("GET", "", "公开的公司列表（招募中、有空位）"),
        ("POST", "", "创建公司（创建者即队长）"),
        ("GET", "/{company}", "公开公司主页（按 id 或 slug）"),
        ("PATCH", "/{company_id}", "编辑公司资料（任意成员）"),
        ("GET", "/{company_id}/members", "成员名单"),
        ("DELETE", "/{company_id}/members/{user_id}", "移除成员（队长）"),
        ("GET", "/{company_id}/invitations", "待处理邀请（队长）"),
        ("POST", "/{company_id}/invitations", "邀请队友（队长）"),
        ("DELETE", "/{company_id}/invitations/{invitation_id}", "撤回邀请（队长）"),
        ("POST", "/{company_id}/invitations/{invitation_id}/deliveries", "重发邀请／激活链接"),
        ("POST", "/{company_id}/invite-links", "生成可分享的邀请链接（队长）"),
        ("DELETE", "/{company_id}/invite-links", "停用邀请链接（队长）"),
        ("GET", "/{company_id}/join-requests", "待处理的入队申请"),
        ("POST", "/{company_id}/join-requests", "申请加入某公司"),
        ("POST", "/{company_id}/join-requests/{request_id}/acceptance", "同意入队申请"),
        ("POST", "/{company_id}/join-requests/{request_id}/decline", "拒绝入队申请"),
        ("POST", "/{company_id}/captain-transfers", "移交队长"),
        ("POST", "/{company_id}/submitter-appointments", "指定作品提交人"),
        ("POST", "/{company_id}/leavings", "退出公司"),
        ("PUT", "/{company_id}/roster-lock", "锁定／解锁名单"),
        ("POST", "/{company_id}/dissolutions", "强制解散公司（管理台）"),
    ],
    tag="companies",
)
