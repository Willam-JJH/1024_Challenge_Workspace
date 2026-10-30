"""管理台：账号。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/admin/users", tags=["admin"])

register_stubs(
    router,
    [
        ("GET", "", "搜索账号（邮箱、英文名、显示名；支持筛选与排序）"),
        ("POST", "", "创建账号（待激活）并发送邀请邮件"),
        ("GET", "/{user_id}", "账号档案：资料、团队、作品、投票、密钥、用量、会话"),
        ("PUT", "/{user_id}/role", "修改全局角色（管理员需超管；不能改自己）"),
        ("PUT", "/{user_id}/mode", "修改参与方式（线上／线下／两者）"),
        ("PUT", "/{user_id}/locale", "设置该账号的邮件语言"),
        ("POST", "/{user_id}/suspensions", "停用账号：全端退出、停用 LLM、硬币失效"),
        ("DELETE", "/{user_id}/suspensions", "恢复账号"),
        ("POST", "/{user_id}/sign-outs", "强制退出全部会话与已连接应用"),
        ("POST", "/{user_id}/activation-deliveries", "重发待激活邀请"),
        ("DELETE", "/{user_id}", "移除账号（保留数据／退出公司／彻底删除）"),
        ("GET", "/{user_id}/teams", "该账号所在的团队"),
        ("PUT", "/roles", "按筛选批量修改角色"),
        ("PUT", "/locales", "按筛选批量设置邮件语言"),
        ("DELETE", "", "按勾选或筛选批量移除账号"),
        ("GET", "/export.csv", "按筛选导出 CSV（留痕）"),
    ],
    tag="admin",
    auth="staff",
)
