"""创建一个管理员账号（首次部署用）。

用法（容器内）::

    docker compose -f infra/docker-compose.prod.yml run --rm api \
        python scripts/create_admin.py --email you@example.com --name 你的名字

口令交互输入；账号已存在时只提升角色。
"""

import argparse
import getpass
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from sqlalchemy import select  # noqa: E402

from app.core.db import SessionLocal  # noqa: E402
from app.core.security import hash_password  # noqa: E402
from app.models.identity import User  # noqa: E402


def main() -> None:
    parser = argparse.ArgumentParser()
    parser.add_argument("--email", required=True)
    parser.add_argument("--name", required=True)
    args = parser.parse_args()

    email = args.email.lower()
    with SessionLocal() as db:
        user = db.scalar(select(User).where(User.email == email))
        if user is None:
            password = getpass.getpass("口令（至少 8 位）：")
            if len(password) < 8:
                raise SystemExit("口令太短")
            user = User(
                email=email,
                password_hash=hash_password(password),
                display_name=args.name,
                role="admin",
                status="active",
            )
            db.add(user)
            print(f"已创建管理员 {email}")
        else:
            user.role = "admin"
            user.status = "active"
            print(f"已把 {email} 提升为管理员")
        db.commit()


if __name__ == "__main__":
    main()
