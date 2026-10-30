"""上传素材。"""

from sqlalchemy import ForeignKey, Integer, String
from sqlalchemy.orm import Mapped, mapped_column

from app.models.base import BaseModel


class MediaAsset(BaseModel):
    __tablename__ = "media_assets"

    owner_id: Mapped[str] = mapped_column(ForeignKey("users.id", ondelete="CASCADE"), index=True)
    company_id: Mapped[str | None] = mapped_column(
        ForeignKey("companies.id", ondelete="SET NULL"), nullable=True
    )
    filename: Mapped[str] = mapped_column(String(200))
    content_type: Mapped[str] = mapped_column(String(120), default="application/octet-stream")
    size: Mapped[int] = mapped_column(Integer, default=0)
    # submission | avatar | sponsor_logo | pitch
    purpose: Mapped[str] = mapped_column(String(32), default="submission")
    alt: Mapped[str | None] = mapped_column(String(300), nullable=True)
    # pending | ready | removed
    status: Mapped[str] = mapped_column(String(16), default="pending")
    storage_key: Mapped[str] = mapped_column(String(300), default="")
