# 多阶段构建：静态二进制 + 最小运行镜像。
FROM golang:1.27-alpine AS build

# 国内网络可传 --build-arg GOPROXY=https://goproxy.cn,direct
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=${GOPROXY}

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22

# 未来接邮件/LLM 网关时需要根证书。
# 上传目录必须预建并归属 app：命名卷首次挂载时会继承镜像里目录的属主，
# 不预建的话 Docker 建成 root:root，非 root 进程写不进去。
RUN apk add --no-cache ca-certificates \
    && adduser -D -u 10001 app \
    && mkdir -p /data/uploads \
    && chown -R app:app /data
COPY --from=build /out/api /api

USER app
# 未显式配置时也不要往工作目录（/）里写
ENV APP_UPLOAD_DIR=/data/uploads
EXPOSE 8000
ENTRYPOINT ["/api"]
