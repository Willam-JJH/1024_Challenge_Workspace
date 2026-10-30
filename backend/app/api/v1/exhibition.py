"""展览：动态流、评论、广告、参观记录印章与介绍任务。"""

from datetime import UTC, datetime

from fastapi import APIRouter, Depends
from sqlalchemy import func, select
from sqlalchemy.orm import Session as DbSession

from app.core.db import get_db
from app.core.deps import Principal, require_auth
from app.core.errors import Conflict, Forbidden, NotFound, Unprocessable
from app.models.event import Company
from app.models.exhibition import (
    Collaboration,
    Comment,
    CommentReport,
    Introduction,
    Promotion,
    Visit,
)
from app.models.identity import User
from app.models.project import Project
from app.schemas.common import OkOut
from app.schemas.exhibition import (
    CollaborationOut,
    CommentIn,
    CommentOut,
    FeedItem,
    IntroductionOut,
    IntroductionReplyIn,
    IntroductionTextIn,
    PassportOut,
    PromotionIn,
    PromotionOut,
    StampOut,
    VisitIn,
    VisitOut,
)
from app.services import companies as company_service

router = APIRouter(prefix="/exhibition", tags=["exhibition"])

PASSPORT_NEEDED = 3
MAX_LIVE_PROMOTIONS = 3


def _comment_out(db: DbSession, comment: Comment) -> CommentOut:
    author = db.get(User, comment.author_id)
    return CommentOut(
        id=comment.id,
        project_id=comment.project_id,
        author_id=comment.author_id,
        author_name=author.display_name if author else "已注销",
        parent_id=comment.parent_id,
        body=comment.body,
        created_at=comment.created_at,
    )


def _my_company_id(db: DbSession, user_id: str) -> str:
    membership = company_service.my_membership(db, user_id)
    if membership is None:
        raise Forbidden("需要先加入一家公司")
    return membership.company_id


# --- 动态流 -----------------------------------------------------------------


@router.get("/feed", response_model=list[FeedItem], summary="活动流：新作品与广告")
def feed(db: DbSession = Depends(get_db)) -> list[FeedItem]:
    items: list[FeedItem] = []

    projects = db.execute(
        select(Project, Company)
        .join(Company, Company.id == Project.company_id)
        .where(Project.status == "published")
        .order_by(Project.updated_at.desc())
        .limit(20)
    ).all()
    for project, company in projects:
        items.append(
            FeedItem(
                type="project",
                title=project.title,
                company_name=company.name,
                at=project.updated_at,
            )
        )

    promotions = db.execute(
        select(Promotion, Company)
        .join(Company, Company.id == Promotion.company_id)
        .where(Promotion.status == "live")
        .order_by(Promotion.created_at.desc())
        .limit(20)
    ).all()
    for promotion, company in promotions:
        items.append(
            FeedItem(
                type="promotion",
                title=promotion.body[:80],
                company_name=company.name,
                at=promotion.created_at,
            )
        )

    items.sort(key=lambda item: item.at, reverse=True)
    return items[:30]


# --- 评论 -------------------------------------------------------------------


@router.get(
    "/projects/{project_id}/comments",
    response_model=list[CommentOut],
    summary="作品的评论串（时间正序）",
)
def list_comments(project_id: str, db: DbSession = Depends(get_db)) -> list[CommentOut]:
    rows = db.scalars(
        select(Comment)
        .where(Comment.project_id == project_id, Comment.status == "visible")
        .order_by(Comment.created_at)
    ).all()
    return [_comment_out(db, row) for row in rows]


@router.post(
    "/projects/{project_id}/comments",
    response_model=CommentOut,
    status_code=201,
    summary="发表评论或回复",
)
def create_comment(
    project_id: str,
    payload: CommentIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> CommentOut:
    project = db.get(Project, project_id)
    if project is None:
        raise NotFound("作品不存在")
    if payload.parent_id is not None:
        parent = db.get(Comment, payload.parent_id)
        if parent is None or parent.project_id != project.id or parent.status != "visible":
            raise Unprocessable("回复的评论不存在")
        if parent.parent_id is not None:
            raise Unprocessable("只能回复顶层评论")

    row = Comment(
        project_id=project.id,
        author_id=principal.user.id,
        parent_id=payload.parent_id,
        body=payload.body,
    )
    db.add(row)
    db.flush()
    return _comment_out(db, row)


def _own_comment(db: DbSession, comment_id: str, principal: Principal) -> Comment:
    row = db.get(Comment, comment_id)
    if row is None or row.status == "deleted":
        raise NotFound("评论不存在")
    if row.author_id != principal.user.id and principal.user.role not in {"staff", "admin"}:
        raise Forbidden("只能操作自己的评论")
    return row


@router.patch("/comments/{comment_id}", response_model=CommentOut, summary="编辑我的评论")
def edit_comment(
    comment_id: str,
    payload: CommentIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> CommentOut:
    row = _own_comment(db, comment_id, principal)
    row.body = payload.body
    db.flush()
    return _comment_out(db, row)


@router.delete("/comments/{comment_id}", response_model=OkOut, summary="删除我的评论")
def delete_comment(
    comment_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _own_comment(db, comment_id, principal)
    row.status = "deleted"
    return OkOut()


@router.post(
    "/comments/{comment_id}/reports", response_model=OkOut, summary="举报评论（每人一次）"
)
def report_comment(
    comment_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    comment = db.get(Comment, comment_id)
    if comment is None or comment.status != "visible":
        raise NotFound("评论不存在")
    if comment.author_id == principal.user.id:
        raise Conflict("不能举报自己的评论")
    existing = db.scalar(
        select(CommentReport).where(
            CommentReport.comment_id == comment.id,
            CommentReport.reporter_id == principal.user.id,
        )
    )
    if existing is not None:
        raise Conflict("已经举报过这条评论")
    db.add(CommentReport(comment_id=comment.id, reporter_id=principal.user.id))
    return OkOut()


# --- 广告 -------------------------------------------------------------------


@router.get("/promotions", response_model=list[PromotionOut], summary="广告墙与跑马灯")
def list_promotions(db: DbSession = Depends(get_db)) -> list[PromotionOut]:
    now = datetime.now(UTC)
    rows = db.execute(
        select(Promotion, Company)
        .join(Company, Company.id == Promotion.company_id)
        .where(
            Promotion.status == "live",
            (Promotion.expires_at.is_(None)) | (Promotion.expires_at > now),
        )
        .order_by(Promotion.created_at.desc())
        .limit(60)
    ).all()
    return [
        PromotionOut(
            id=promotion.id,
            company_id=company.id,
            company_name=company.name,
            kind=promotion.kind,
            body=promotion.body,
            created_at=promotion.created_at,
        )
        for promotion, company in rows
    ]


@router.post("/promotions", response_model=PromotionOut, status_code=201, summary="发布广告")
def create_promotion(
    payload: PromotionIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> PromotionOut:
    company_id = _my_company_id(db, principal.user.id)
    live = int(
        db.scalar(
            select(func.count())
            .select_from(Promotion)
            .where(Promotion.company_id == company_id, Promotion.status == "live")
        )
        or 0
    )
    if live >= MAX_LIVE_PROMOTIONS:
        raise Conflict(f"每家公司最多 {MAX_LIVE_PROMOTIONS} 条在线广告")

    row = Promotion(company_id=company_id, kind=payload.kind, body=payload.body)
    db.add(row)
    db.flush()
    company = db.get(Company, company_id)
    return PromotionOut(
        id=row.id,
        company_id=company_id,
        company_name=company.name if company else "",
        kind=row.kind,
        body=row.body,
        created_at=row.created_at,
    )


def _own_promotion(db: DbSession, promotion_id: str, principal: Principal) -> Promotion:
    row = db.get(Promotion, promotion_id)
    if row is None:
        raise NotFound("广告不存在")
    membership = company_service.membership_of(db, row.company_id, principal.user.id)
    if membership is None and principal.user.role not in {"staff", "admin"}:
        raise Forbidden("只能操作本公司的广告")
    return row


@router.patch("/promotions/{promotion_id}", response_model=OkOut, summary="编辑我公司的广告")
def edit_promotion(
    promotion_id: str,
    payload: PromotionIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _own_promotion(db, promotion_id, principal)
    row.kind = payload.kind
    row.body = payload.body
    return OkOut()


@router.delete("/promotions/{promotion_id}", response_model=OkOut, summary="撤下我公司的广告")
def delete_promotion(
    promotion_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = _own_promotion(db, promotion_id, principal)
    row.status = "hidden"
    return OkOut()


# --- 参观记录印章 ---------------------------------------------------------------


@router.get("/visits", response_model=PassportOut, summary="我的参观参观记录：印章与完成度")
def my_passport(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> PassportOut:
    event = company_service.current_event(db)
    rows = db.execute(
        select(Visit, Project, Company)
        .join(Project, Project.id == Visit.project_id)
        .join(Company, Company.id == Project.company_id)
        .where(Visit.visitor_id == principal.user.id, Visit.event_id == event.id)
        .order_by(Visit.created_at)
    ).all()
    items = [
        VisitOut(
            project_id=project.id,
            company_name=company.name,
            project_title=project.title,
            note=visit.note,
            created_at=visit.created_at,
        )
        for visit, project, company in rows
    ]
    distinct_companies = {item.company_name for item in items}
    return PassportOut(
        items=items,
        needed=PASSPORT_NEEDED,
        complete=len(distinct_companies) >= PASSPORT_NEEDED,
    )


@router.put("/visits/{project_id}", response_model=OkOut, summary="为作品打卡")
def stamp(
    project_id: str,
    payload: VisitIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    project = db.get(Project, project_id)
    if project is None or project.status != "published":
        raise NotFound("作品不存在或尚未发布")
    if company_service.membership_of(db, project.company_id, principal.user.id) is not None:
        raise Forbidden("不能给自己公司的作品打卡")

    event = company_service.current_event(db)
    row = db.scalar(
        select(Visit).where(
            Visit.event_id == event.id,
            Visit.project_id == project.id,
            Visit.visitor_id == principal.user.id,
        )
    )
    if row is None:
        db.add(
            Visit(
                event_id=event.id,
                project_id=project.id,
                visitor_id=principal.user.id,
                note=payload.note,
            )
        )
    else:
        row.note = payload.note
    return OkOut()


@router.delete("/visits/{project_id}", response_model=OkOut, summary="移除印章")
def remove_stamp(
    project_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.scalar(
        select(Visit).where(Visit.project_id == project_id, Visit.visitor_id == principal.user.id)
    )
    if row is not None:
        db.delete(row)
    return OkOut()


@router.get(
    "/companies/{company_id}/visits",
    response_model=list[StampOut],
    summary="谁为我们公司盖过章",
)
def company_stamps(
    company_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> list[StampOut]:
    membership = company_service.membership_of(db, company_id, principal.user.id)
    if membership is None and principal.user.role not in {"staff", "admin"}:
        raise Forbidden("只有成员可以查看")
    rows = db.execute(
        select(Visit, User)
        .join(Project, Project.id == Visit.project_id)
        .join(User, User.id == Visit.visitor_id)
        .where(Project.company_id == company_id)
        .order_by(Visit.created_at.desc())
    ).all()
    return [
        StampOut(visitor_name=user.display_name, note=visit.note, created_at=visit.created_at)
        for visit, user in rows
    ]


# --- 介绍任务 ---------------------------------------------------------------


def _intro_out(db: DbSession, row: Introduction, my_company_id: str) -> IntroductionOut:
    introducer = db.get(Company, row.introducer_company_id)
    target = db.get(Company, row.target_company_id)
    return IntroductionOut(
        id=row.id,
        role="introducer" if row.introducer_company_id == my_company_id else "target",
        introducer_company=introducer.name if introducer else "",
        target_company=target.name if target else "",
        text=row.text,
        reply=row.reply,
        status=row.status,
    )


@router.get(
    "/introductions", response_model=list[IntroductionOut], summary="与我公司相关的介绍任务"
)
def my_introductions(
    principal: Principal = Depends(require_auth), db: DbSession = Depends(get_db)
) -> list[IntroductionOut]:
    company_id = _my_company_id(db, principal.user.id)
    rows = db.scalars(
        select(Introduction)
        .where(
            (Introduction.introducer_company_id == company_id)
            | (Introduction.target_company_id == company_id)
        )
        .order_by(Introduction.created_at)
    ).all()
    return [_intro_out(db, row, company_id) for row in rows]


@router.put(
    "/introductions/{introduction_id}/text",
    response_model=IntroductionOut,
    summary="撰写奖项日介绍",
)
def set_introduction_text(
    introduction_id: str,
    payload: IntroductionTextIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> IntroductionOut:
    row = db.get(Introduction, introduction_id)
    if row is None:
        raise NotFound("介绍任务不存在")
    company_id = _my_company_id(db, principal.user.id)
    if row.introducer_company_id != company_id:
        raise Forbidden("只有介绍方可以撰写")
    row.text = payload.text
    db.flush()
    return _intro_out(db, row, company_id)


@router.put(
    "/introductions/{introduction_id}/reply",
    response_model=IntroductionOut,
    summary="被介绍公司作答",
)
def set_introduction_reply(
    introduction_id: str,
    payload: IntroductionReplyIn,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> IntroductionOut:
    row = db.get(Introduction, introduction_id)
    if row is None:
        raise NotFound("介绍任务不存在")
    company_id = _my_company_id(db, principal.user.id)
    if row.target_company_id != company_id:
        raise Forbidden("只有被介绍的公司可以回复")
    row.reply = payload.reply
    db.flush()
    return _intro_out(db, row, company_id)


@router.delete(
    "/introductions/{introduction_id}/reply",
    response_model=OkOut,
    summary="删除我方回复",
)
def delete_introduction_reply(
    introduction_id: str,
    principal: Principal = Depends(require_auth),
    db: DbSession = Depends(get_db),
) -> OkOut:
    row = db.get(Introduction, introduction_id)
    if row is None:
        raise NotFound("介绍任务不存在")
    company_id = _my_company_id(db, principal.user.id)
    if row.target_company_id != company_id:
        raise Forbidden("只有被介绍的公司可以删除回复")
    row.reply = None
    return OkOut()


@router.get(
    "/collaborations", response_model=list[CollaborationOut], summary="已公布的合作"
)
def list_collaborations(db: DbSession = Depends(get_db)) -> list[CollaborationOut]:
    event = company_service.current_event(db)
    rows = db.scalars(
        select(Collaboration)
        .where(
            Collaboration.event_id == event.id,
            Collaboration.status == "published",
        )
        .order_by(Collaboration.created_at.desc())
    ).all()
    out = []
    for row in rows:
        source = db.get(Company, row.from_company_id)
        target = db.get(Company, row.to_company_id)
        out.append(
            CollaborationOut(
                id=row.id,
                from_company=source.name if source else "",
                to_company=target.name if target else "",
                kind=row.kind,
                body=row.body,
                status=row.status,
            )
        )
    return out
