FROM python:3.12-slim

ENV PYTHONUNBUFFERED=1 \
    PYTHONDONTWRITEBYTECODE=1 \
    UV_SYSTEM_PYTHON=1

RUN pip install --no-cache-dir uv

# 上传卷首次挂载会继承镜像里目录的属主；预建并交给 10001，与 go-api 保持一致，
# 避免 api 先创建命名卷时把卷建成 root:root（那样 Go 侧写不进去）。
RUN mkdir -p /data/uploads && chown -R 10001:10001 /data

WORKDIR /app
COPY pyproject.toml ./
RUN uv pip install --system -r pyproject.toml

COPY app ./app
COPY migrations ./migrations
COPY scripts ./scripts
COPY alembic.ini ./

EXPOSE 8000
# 两个 worker 各带自己的连接池；WEB_CONCURRENCY 可按机器规格覆盖。
CMD ["sh", "-c", "exec uvicorn app.main:app --host 0.0.0.0 --port 8000 --workers ${WEB_CONCURRENCY:-2}"]
