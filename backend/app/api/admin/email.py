"""管理台：邮件中心。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/admin/email", tags=["admin"])

register_stubs(
    router,
    [
        ("GET", "/broadcasts", "广播列表（最新在前）"),
        ("POST", "/broadcasts", "保存广播草稿"),
        ("PATCH", "/broadcasts/{broadcast_id}", "编辑草稿或已排期广播"),
        ("DELETE", "/broadcasts/{broadcast_id}", "删除草稿或已取消的广播"),
        ("POST", "/broadcasts/{broadcast_id}/cancellations", "取消草稿或已排期广播"),
        ("POST", "/broadcasts/{broadcast_id}/schedules", "立即发送或定时发送"),
        ("POST", "/broadcasts/{broadcast_id}/tests", "发一封给自己的测试邮件"),
        ("POST", "/broadcasts/previews", "按收件人视角渲染广播内容"),
        ("POST", "/audience-estimates", "按语言估算受众规模（受每日上限约束）"),
        ("GET", "/messages", "邮件日志（最新在前，按状态计数）"),
        ("POST", "/messages/{message_id}/resends", "重新排队发送失败的邮件"),
        ("GET", "/templates", "全部邮件模板"),
        ("POST", "/template-previews", "用示例数据渲染模板（中英文）"),
        ("GET", "/setup", "邮件驱动与每日配额"),
        ("GET", "/quota", "邮件服务商配额"),
    ],
    tag="admin",
    auth="staff",
)
