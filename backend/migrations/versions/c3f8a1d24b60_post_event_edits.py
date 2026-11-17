"""allow project edits after the event ends

Revision ID: c3f8a1d24b60
Revises: b7c1e5a9d240
Create Date: 2026-11-17 16:40:12.904517

比赛结束（投票与路演都结束）之后，团队仍然可以继续提交作品的新版本。
这一行为由赛事级的 `post_event_edits` 控制，默认开启；关掉之后该届作品在
`ends_at` 之后就变成只读。

放在赛事上而不是全局配置：不同届的诉求不一样，往届可能需要冻结，当届需要继续打磨。
"""
from typing import Sequence, Union

from alembic import op
import sqlalchemy as sa
import app.models.types  # noqa: F401 - 列类型引用 app.models.types.UTCDateTime


# revision identifiers, used by Alembic.
revision: str = 'c3f8a1d24b60'
down_revision: Union[str, Sequence[str], None] = 'b7c1e5a9d240'
branch_labels: Union[str, Sequence[str], None] = None
depends_on: Union[str, Sequence[str], None] = None


def upgrade() -> None:
    """Upgrade schema."""
    with op.batch_alter_table('events', schema=None) as batch_op:
        # 现有赛事一律设为开启：默认行为是「赛后继续可编辑」。
        batch_op.add_column(
            sa.Column('post_event_edits', sa.Boolean(), nullable=False,
                      server_default=sa.text('true'))
        )
    with op.batch_alter_table('events', schema=None) as batch_op:
        # 与其它列保持一致：只由应用写入默认值，库里不留 server default。
        batch_op.alter_column('post_event_edits', server_default=None)


def downgrade() -> None:
    """Downgrade schema."""
    with op.batch_alter_table('events', schema=None) as batch_op:
        batch_op.drop_column('post_event_edits')
