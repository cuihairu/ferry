# ferry-panel：构建面板静态产物后由 Caddy 托管并反代后端。
FROM node:24-alpine AS build
WORKDIR /src
COPY pnpm-workspace.yaml package.json pnpm-lock.yaml ./
COPY panel/package.json panel/
RUN corepack enable && pnpm install --frozen-lockfile
COPY panel/ panel/
RUN cd panel && pnpm build

FROM caddy:2-alpine
COPY --from=build /src/panel/dist /srv
COPY deploy/Caddyfile /etc/caddy/Caddyfile
EXPOSE 80
