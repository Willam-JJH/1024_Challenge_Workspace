"""season archiving and per-event enrollment

Revision ID: b7c1e5a9d240
Revises: eae3777bb79c
Create Date: 2026-11-17 10:12:07.318241

赛事从「一次性活动」变成「每年一届」之后需要两样东西：

* `events.archived_at`：赛季正式收口的时刻。比赛结束满一个月由后台任务写入，
  写入后该届作品进入「前序作品」、报名与投票开关被关闭、并且不再接受任何写入。
  用独立列而不是复用 `stage`，是因为 `stage` 是工作人员手工排期用的展示字段，
  系统判断不能依赖它。
* `event_enrollments`：按届记录谁报名成了参赛者、谁报名成了观众。原来的
  `users.mode` 是全局字段，跨届会串味。
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
import app.models.types  # noqa: F401 - 列类型引用 app.models.types.UTCDateTime


# revision identifiers, used by Alembic.
revision: str = 'b7c1e5a9d240'
down_revision: Union[str, Sequence[str], None] = 'eae3777bb79c'
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    """Upgrade schema."""
    with op.batch_alter_table('events', schema=None) as batch_op:
        batch_op.add_column(
            sa.Column('archived_at', app.models.types.UTCDateTime(), nullable=True)
        )

    op.create_table(
        'event_enrollments',
        sa.Column('event_id', sa.String(length=32), nullable=False),
        sa.Column('user_id', sa.String(length=32), nullable=False),
        # participant | audience
        sa.Column('kind', sa.String(length=16), nullable=False),
        # active | withdrawn
        sa.Column('status', sa.String(length=16), nullable=False),
        sa.Column('id', sa.String(length=32), nullable=False),
        sa.Column('created_at', app.models.types.UTCDateTime(), nullable=False),
        sa.Column('updated_at', app.models.types.UTCDateTime(), nullable=False),
        sa.ForeignKeyConstraint(['event_id'], ['events.id'], ondelete='CASCADE'),
        sa.ForeignKeyConstraint(['user_id'], ['users.id'], ondelete='CASCADE'),
        sa.PrimaryKeyConstraint('id'),
        sa.UniqueConstraint('event_id', 'user_id', name='uq_event_enrollment'),
    )
    with op.batch_alter_table('event_enrollments', schema=None) as batch_op:
        batch_op.create_index(
            batch_op.f('ix_event_enrollments_event_id'), ['event_id'], unique=False
        )
        batch_op.create_index(
            batch_op.f('ix_event_enrollments_user_id'), ['user_id'], unique=False
        )


def downgrade() -> None:
    """Downgrade schema."""
    with op.batch_alter_table('event_enrollments', schema=None) as batch_op:
        batch_op.drop_index(batch_op.f('ix_event_enrollments_user_id'))
        batch_op.drop_index(batch_op.f('ix_event_enrollments_event_id'))
    op.drop_table('event_enrollments')

    with op.batch_alter_table('events', schema=None) as batch_op:
        batch_op.drop_column('archived_at')
