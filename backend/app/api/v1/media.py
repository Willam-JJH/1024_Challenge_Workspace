"""上传与素材。"""

from fastapi import APIRouter

from app.core.stubs import register_stubs

router = APIRouter(tags=["media"])

register_stubs(
    router,
    [
        ("POST", "/media/assets", "申请上传，换取 PUT 地址"),
        ("GET", "/media/assets/{asset_id}", "素材元数据"),
        ("DELETE", "/media/assets/{asset_id}", "删除未被引用的素材"),
        ("PATCH", "/media/assets/{asset_id}", "设置替代文本"),
        ("PUT", "/media/assets/{asset_id}/content", "上传字节（本地存储驱动）"),
        ("PUT", "/media/assets/{asset_id}/parts/{part}", "分片上传（本地存储驱动）"),
        ("POST", "/media/assets/{asset_id}/completions", "完成上传：嗅探类型、去除 EXIF"),
        ("GET", "/files/{asset_id}", "读取素材原始文件"),
        ("GET", "/files/{asset_id}/{variant}", "读取素材变体（缩略图等）"),
    ],
    tag="media",
)
