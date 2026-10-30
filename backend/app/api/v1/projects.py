"""作品：公开画廊、草稿、编辑锁、发布与版本。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(prefix="/projects", tags=["projects"])

register_stubs(
    router,
    [
        ("GET", "", "已发布作品画廊（格式、标签、搜索、排序）"),
        ("GET", "/draft-schema", "草稿字段的 JSON Schema（供代理使用）"),
        ("GET", "/{project_id}", "草稿视图（成员与管理员）"),
        ("PATCH", "/{project_id}/draft", "自动保存草稿（需持有编辑锁与当前修订号）"),
        ("POST", "/{project_id}/edit-sessions", "获取编辑锁（他人持有时返回 409）"),
        ("POST", "/{project_id}/edit-sessions/{session_id}/heartbeats", "续租编辑锁"),
        ("DELETE", "/{project_id}/edit-sessions/{session_id}", "释放编辑锁"),
        ("POST", "/{project_id}/publications", "提交／更新作品（快照为新版本）"),
        ("POST", "/{project_id}/agent-draft-approvals", "标记代理草稿已由人复核"),
        ("GET", "/{project_id}/versions", "已发布版本（最新在前）"),
        ("GET", "/{project_id}/versions/{version}", "单个已发布版本"),
        ("POST", "/{project_id}/versions/{version}/restorations", "把某个版本复制回草稿"),
        ("GET", "/{project_id}/attendance", "Pitching Day 出席意向"),
        ("PUT", "/{project_id}/attendance", "回答出席问题（提交人或队长）"),
    ],
    tag="projects",
)
