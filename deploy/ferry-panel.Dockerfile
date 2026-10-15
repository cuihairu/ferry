# ferry-panel：构建用户端静态产物后由 Caddy 托管并反代后端。
# 与 ferry-dash.Dockerfile 同构：同一份 pnpm workspace install，只构建 panel/
# 子包；产物同样落 /srv，复用 deploy/Caddyfile（反代 /api /sub /agent
# /feed.xml 到 ferry-server，其余路径走 history 回退）。
FROM node:24-alpine AS build
WORKDIR /src
# workspace 三包的 package.json 先落齐再 install（pnpm-workspace.yaml 声明
# dash/panel/docs，缺任一即 install 报错；对齐 CI 根目录 install 口径，见
# .github/workflows/test.yml install 步）。
COPY pnpm-workspace.yaml pnpm-lock.yaml ./
COPY dash/package.json dash/
COPY panel/package.json panel/
COPY docs/package.json docs/
ENV PNPM_DANGEROUSLY_ALLOW_ALL_BUILDS=true
RUN corepack enable && corepack prepare pnpm@10 --activate \
  && pnpm install --frozen-lockfile --config.minimum-release-age=0 --config.dangerouslyAllowAllBuilds=true
COPY panel/ panel/
RUN cd panel && pnpm build

FROM caddy:2-alpine
COPY --from=build /src/panel/dist /srv
COPY deploy/Caddyfile /etc/caddy/Caddyfile
EXPOSE 80
