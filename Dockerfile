# ssb + sing-box，多阶段构建。
# 运行需要: --network host --cap-add NET_ADMIN --device /dev/net/tun（见 docker-compose.yml）
FROM golang:1.25-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/ssb ./cmd/ssb

# 直接从官方 release 取 sing-box 内核（TARGETARCH: amd64/arm64）
FROM alpine:3.20 AS singbox
ARG TARGETARCH
ARG SINGBOX_VERSION=1.13.12
RUN apk add --no-cache curl tar && \
    curl -fsSL "https://github.com/SagerNet/sing-box/releases/download/v${SINGBOX_VERSION}/sing-box-${SINGBOX_VERSION}-linux-${TARGETARCH}.tar.gz" \
      | tar -xz -C /tmp && \
    mv /tmp/sing-box-*/sing-box /usr/local/bin/sing-box

FROM alpine:3.20
RUN apk add --no-cache ca-certificates tzdata libcap
WORKDIR /app
COPY --from=build /out/ssb /app/ssb
# 内核放 PATH（/app/data 会被 compose 卷挂载遮盖，不能放那里）
COPY --from=singbox /usr/local/bin/sing-box /usr/local/bin/sing-box
ENV SSB_DIR=/app
ENTRYPOINT ["/app/ssb"]
CMD ["help"]
