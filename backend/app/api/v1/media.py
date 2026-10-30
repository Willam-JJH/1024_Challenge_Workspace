"""素材：申请上传、写入字节、分片、元数据与读取。

存储驱动目前只有本地磁盘（``APP_UPLOAD_DIR``，默认 ``backend/uploads``）。
对象存储（R2/S3）以后补一个实现即可，接口不变。
"""

import re
from pathlib import Path

from fastapi import APIRouter, Depends, Request
from fastapi.responses import FileResponse
from sqlalchemy.orm import Session as DbSession

from app.core.config import settings
from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import ApiError, Forbidden, NotFound
from app.models.media import MediaAsset
from app.schemas.common import OkOut
from app.schemas.media import AltIn, AssetCreateIn, AssetCreatedOut, AssetOut

router = APIRouter(tags=["media"])

SAFE_NAME = re.compile(r"[^A-Za-z0-9._-]+")


def _upload_root() -> Path:
    root = Path(settings.upload_dir)
    if not root.is_absolute():
        root = Path(__file__).resolve().parents[3] / settings.upload_dir
    root.mkdir(parents=True, exist_ok=True)
    return root


def _safe(filename: str) -> str:
    name = SAFE_NAME.sub("_", Path(filename).name).strip("._") or "file"
    return name[:120]


def _asset(db: DbSession, asset_id: str) -> MediaAsset:
    row = db.get(MediaAsset, asset_id)
    if row is None or row.status == "removed":
        raise NotFound("素材不存在")
    return row


def _sniff(head: bytes, declared: str) -> str:
    if head.startswith(b"\x89PNG\r\n\x1a\n"):
        return "image/png"
    if head.startswith(b"\xff\xd8\xff"):
        return "image/jpeg"
    if head.startswith(b"GIF8"):
        return "image/gif"
    if head[:4] == b"RIFF" and head[8:12] == b"WEBP":
        return "image/webp"
    if head.startswith(b"%PDF"):
        return "application/pdf"
    return declared or "application/octet-stream"


@router.post(
    "/media/assets", response_model=AssetCreatedOut, status_code=201, summary="申请上传"
)
def create_asset(
    payload: AssetCreateIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> AssetCreatedOut:
    row = MediaAsset(
        owner_id=principal.user.id,
        filename=_safe(payload.filename),
        content_type=payload.content_type,
        size=payload.size,
        purpose=payload.purpose,
    )
    db.add(row)
    db.flush()
    row.storage_key = f"{row.id}/{row.filename}"
    db.flush()
    return AssetCreatedOut(
        asset=AssetOut.model_validate(row),
        upload_url=f"{settings.api_prefix}/media/assets/{row.id}/content",
    )


def _write(db: DbSession, row: MediaAsset, data: bytes) -> None:
    if not data:
        raise ApiError("内容为空", code="empty_body", status_code=422)
    if len(data) > settings.max_upload_bytes:
        raise ApiError("文件过大", code="too_large", status_code=413)
    path = _upload_root() / row.storage_key
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    row.size = len(data)
    row.content_type = _sniff(data[:16], row.content_type)
    row.status = "ready"
    db.flush()


def _own(row: MediaAsset, principal: Principal) -> None:
    if row.owner_id != principal.user.id and principal.user.role not in {"staff", "admin"}:
        raise Forbidden("只能操作自己的上传")


@router.put("/media/assets/{asset_id}/content", response_model=AssetOut, summary="上传字节")
async def upload_content(
    asset_id: str,
    request: Request,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> AssetOut:
    row = _asset(db, asset_id)
    _own(row, principal)
    _write(db, row, await request.body())
    return AssetOut.model_validate(row)


@router.put(
    "/media/assets/{asset_id}/parts/{part}", response_model=OkOut, summary="上传一个分片"
)
async def upload_part(
    asset_id: str,
    part: int,
    request: Request,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    if part < 1 or part > 10_000:
        raise ApiError("分片序号不合法", code="invalid_part", status_code=422)
    row = _asset(db, asset_id)
    _own(row, principal)
    data = await request.body()
    if len(data) > settings.max_upload_bytes:
        raise ApiError("分片过大", code="too_large", status_code=413)
    path = _upload_root() / f"{row.storage_key}.part{part}"
    path.parent.mkdir(parents=True, exist_ok=True)
    path.write_bytes(data)
    return OkOut()


@router.post(
    "/media/assets/{asset_id}/completions", response_model=AssetOut, summary="完成分片上传"
)
def complete_upload(
    asset_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> AssetOut:
    row = _asset(db, asset_id)
    _own(row, principal)
    root = _upload_root()
    base = root / row.storage_key
    parts = sorted(base.parent.glob(f"{base.name}.part*"))
    if not parts:
        raise ApiError("没有收到任何分片", code="no_parts", status_code=422)
    data = b"".join(part.read_bytes() for part in parts)
    _write(db, row, data)
    for part in parts:
        part.unlink(missing_ok=True)
    return AssetOut.model_validate(row)


@router.get("/media/assets/{asset_id}", response_model=AssetOut, summary="素材元数据")
def get_asset(asset_id: str, db: DbSession = Depends(get_db)) -> AssetOut:
    return AssetOut.model_validate(_asset(db, asset_id))


@router.patch("/media/assets/{asset_id}", response_model=AssetOut, summary="设置替代文本")
def set_alt(
    asset_id: str,
    payload: AltIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> AssetOut:
    row = _asset(db, asset_id)
    _own(row, principal)
    row.alt = payload.alt
    db.flush()
    return AssetOut.model_validate(row)


@router.delete("/media/assets/{asset_id}", response_model=OkOut, summary="删除素材")
def delete_asset(
    asset_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _asset(db, asset_id)
    _own(row, principal)
    row.status = "removed"
    (_upload_root() / row.storage_key).unlink(missing_ok=True)
    return OkOut()


@router.get("/files/{asset_id}", summary="读取素材原始文件")
def read_file(asset_id: str, db: DbSession = Depends(get_db)) -> FileResponse:
    row = _asset(db, asset_id)
    if row.status != "ready":
        raise NotFound("素材尚未完成上传")
    path = _upload_root() / row.storage_key
    if not path.exists():
        raise NotFound("文件名下的内容不存在")
    return FileResponse(path, media_type=row.content_type, filename=row.filename)


@router.get("/files/{asset_id}/{variant}", summary="读取素材变体")
def read_variant(asset_id: str, variant: str, db: DbSession = Depends(get_db)) -> FileResponse:
    _asset(db, asset_id)
    raise ApiError(
        f"变体 {variant} 尚未生成（图片处理管线待接入）",
        code="variant_not_available",
        status_code=404,
    )
