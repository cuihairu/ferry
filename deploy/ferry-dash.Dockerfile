# ferry-dash：构建管理端静态产物后由 Caddy 托管并反代后端。
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
COPY dash/ dash/
RUN cd dash && pnpm build

FROM caddy:2-alpine
COPY --from=build /src/dash/dist /srv
COPY deploy/Caddyfile /etc/caddy/Caddyfile
EXPOSE 80
