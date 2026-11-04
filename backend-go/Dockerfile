# 多阶段构建：静态二进制 + 最小运行镜像。
FROM golang:1.27-alpine AS build

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/api ./cmd/api

FROM alpine:3.22

# 未来接邮件/LLM 网关时需要根证书
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
COPY --from=build /out/api /api

USER app
EXPOSE 8000
ENTRYPOINT ["/api"]
