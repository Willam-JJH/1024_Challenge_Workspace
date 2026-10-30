"""v1 路由汇总。"""

from fastapi import APIRouter

from app.api.admin import companies as admin_companies
from app.api.admin import email as admin_email
from app.api.admin import llm as admin_llm
from app.api.admin import ops as admin_ops
from app.api.admin import users as admin_users
from app.api.v1 import (
    auth,
    companies,
    content,
    events,
    exhibition,
    finder,
    health,
    judging,
    me,
    media,
    oauth,
    offline,
    pitch,
    projects,
    results,
    votes,
)

api_router = APIRouter()

api_router.include_router(health.router)
api_router.include_router(auth.router)
api_router.include_router(me.router)
api_router.include_router(events.router)
api_router.include_router(finder.router)
api_router.include_router(companies.router)
api_router.include_router(projects.router)
api_router.include_router(media.router)
api_router.include_router(votes.router)
api_router.include_router(judging.router)
api_router.include_router(results.router)
api_router.include_router(exhibition.router)
api_router.include_router(pitch.router)
api_router.include_router(offline.router)
api_router.include_router(content.router)
api_router.include_router(oauth.oauth_router)
api_router.include_router(oauth.usage_router)
api_router.include_router(oauth.well_known_router)

api_router.include_router(admin_users.router)
api_router.include_router(admin_companies.router)
api_router.include_router(admin_email.router)
api_router.include_router(admin_llm.router)
api_router.include_router(admin_ops.router)
