"""未迁移端点的统一占位。

每个 router 先用 ``register_stubs`` 把契约挂上：路径、方法、摘要、鉴权都在 OpenAPI
中可见，调用时返回 501 + ``not_implemented``，等实现到位后逐条替换掉即可。
"""

from fastapi import APIRouter, Depends

from app.core.deps import Principal, require_auth, require_staff
from app.core.errors import NotImplementedYet


def register_stubs(
    router: APIRouter,
    specs: list[tuple[str, str, str]],
    *,
    tag: str,
    auth: str = "public",
) -> None:
    """按 (方法, 路径, 摘要) 注册未实现端点。

    auth: ``public`` | ``authenticated`` | ``staff``
    """
    dependencies = []
    if auth == "authenticated":
        dependencies = [Depends(require_auth)]
    elif auth == "staff":
        dependencies = [Depends(require_staff)]

    for method, path, summary in specs:

        def _handler(_summary: str = summary, _path: str = path) -> None:
            raise NotImplementedYet(
                f"接口已定义、实现待迁移：{_summary}",
                details={"path": _path, "status": "contract_only"},
            )

        router.add_api_route(
            path,
            _handler,
            methods=[method.upper()],
            summary=summary,
            tags=[tag],
            dependencies=dependencies,
            include_in_schema=True,
            response_model=None,
        )


__all__ = ["register_stubs", "Principal"]
