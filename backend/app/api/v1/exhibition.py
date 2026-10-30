"""展览：动态流、评论、广告、合作、介绍与护照印章。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/exhibition", tags=["exhibition"])

register_stubs(
    router,
    [
        ("GET", "/feed", "活动流：新作品、合作、介绍与广告"),
        ("GET", "/projects/{project_id}/comments", "作品的评论串（时间正序）"),
        ("POST", "/projects/{project_id}/comments", "发表评论或回复"),
        ("PATCH", "/comments/{comment_id}", "编辑我的评论"),
        ("DELETE", "/comments/{comment_id}", "删除我的评论"),
        ("POST", "/comments/{comment_id}/reports", "举报评论（每人一次）"),
        ("GET", "/promotions", "广告墙与跑马灯"),
        ("POST", "/promotions", "发布广告（每家公司最多三条在线）"),
        ("PATCH", "/promotions/{promotion_id}", "编辑我公司的广告"),
        ("DELETE", "/promotions/{promotion_id}", "撤下我公司的广告"),
        ("GET", "/collaborations", "已公布的合作"),
        ("GET", "/introductions", "我公司的介绍任务"),
        ("PUT", "/introductions/{introduction_id}/reply", "被介绍公司作答"),
        ("DELETE", "/introductions/{introduction_id}/reply", "删除我方回复"),
        ("PUT", "/introductions/{introduction_id}/text", "撰写奖项日介绍（约 150 字）"),
        ("GET", "/visits", "我的护照印章"),
        ("PUT", "/visits/{project_id}", "为作品盖章"),
        ("DELETE", "/visits/{project_id}", "移除印章"),
        ("GET", "/companies/{company_id}/visits", "谁为我们公司盖过章"),
    ],
    tag="exhibition",
)
