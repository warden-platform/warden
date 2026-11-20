# 默认国内镜像源（Gitee Go / 国内网络可直接构建）；海外构建可 --build-arg 换回官方源。
ARG NODE_IMAGE=docker.m.daocloud.io/library/node:22-alpine
ARG NGINX_IMAGE=docker.m.daocloud.io/nginxinc/nginx-unprivileged:1.27-alpine
FROM ${NODE_IMAGE} AS build
ARG NPM_REGISTRY=https://registry.npmmirror.com
WORKDIR /src
COPY package.json package-lock.json ./
RUN npm config set registry "${NPM_REGISTRY}" && npm ci
COPY . .
RUN npm run build

FROM ${NGINX_IMAGE}
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /src/dist /usr/share/nginx/html
EXPOSE 8080
