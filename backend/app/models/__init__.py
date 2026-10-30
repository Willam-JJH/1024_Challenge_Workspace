"""全部 ORM 模型的汇总导入（建表与 Alembic 自动生成依赖它）。"""

from app.models.admin import Announcement, FeatureFlag, Sponsor
from app.models.audit import AuditLog
from app.models.base import BaseModel
from app.models.email import Broadcast, EmailMessage
from app.models.event import Company, Event, Invitation, InviteLink, JoinRequest, Membership
from app.models.exhibition import (
    Collaboration,
    Comment,
    CommentReport,
    Introduction,
    Promotion,
    Visit,
)
from app.models.identity import ApiKey, Session, User
from app.models.judging import Award, Score, ShortlistEntry
from app.models.llm import LlmModel, LlmRoute, LlmUpstream, LlmUsage
from app.models.offline import OfflineRsvp, OfflineSession
from app.models.pitch import PitchMessage, PitchSource
from app.models.project import Project, ProjectEditSession, ProjectVersion
from app.models.verification import VerificationCode
from app.models.vote import Vote

__all__ = [
    "Announcement",
    "ApiKey",
    "AuditLog",
    "BaseModel",
    "Company",
    "Broadcast",
    "Comment",
    "EmailMessage",
    "FeatureFlag",
    "Invitation",
    "Introduction",
    "InviteLink",
    "JoinRequest",
    "Event",
    "Membership",
    "Promotion",
    "Award",
    "LlmModel",
    "LlmRoute",
    "LlmUpstream",
    "LlmUsage",
    "OfflineRsvp",
    "OfflineSession",
    "PitchMessage",
    "PitchSource",
    "Project",
    "ProjectEditSession",
    "ProjectVersion",
    "Score",
    "Sponsor",
    "Session",
    "ShortlistEntry",
    "User",
    "VerificationCode",
    "Visit",
    "Collaboration",
    "CommentReport",
    "Vote",
]
