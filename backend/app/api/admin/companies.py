"""管理台：公司、作品与素材。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(tags=["admin"])

register_stubs(
    router,
    [
        ("GET", "/admin/companies", "当前赛事的公司（名单、队长、作品）"),
        ("GET", "/admin/companies/{company_id}", "公司档案：成员、邀请、作品、名单变更"),
        ("POST", "/admin/companies/{company_id}/dissolutions", "强制解散公司"),
        ("PUT", "/admin/companies/{company_id}/roster-lock", "锁定／解锁某公司名单"),
        ("POST", "/admin/companies/{company_id}/captain-transfers", "指定另一位成员为队长"),
        ("POST", "/admin/companies/{company_id}/submitter-appointments", "移交提交人角色"),
        ("POST", "/admin/members/moves", "把某人移到另一家公司，或移出公司"),
        ("GET", "/admin/projects", "当前赛事全部作品（含草稿）"),
        ("GET", "/admin/projects/{project_id}", "作品档案"),
        ("GET", "/admin/projects/{project_id}/versions", "作品版本列表"),
        ("GET", "/admin/projects/{project_id}/versions/{version}", "单个已发布版本"),
        ("DELETE", "/admin/media/{asset_id}", "下架文件（不论是否被引用）"),
    ],
    tag="admin",
    auth="staff",
)
