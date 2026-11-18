# Warden Web

> **面向**：要改管理端代码的开发者。本文是前端目录与命令速查。
> 完整上手见 [`../docs/quickstart.md`](../docs/quickstart.md)，权限门控规则见 [`../docs/authorization.md`](../docs/authorization.md)，接口见 [`../docs/api.md`](../docs/api.md)，文档地图见 [`../docs/README.md`](../docs/README.md)。

React + TypeScript + Vite 管理端。

## 目录

```text
src/
├── app/              应用入口组件和全局样式
├── shared/           跨功能共享类型
└── main.tsx          浏览器入口
```

全部管理页面均通过 `src/shared/api/` 连接 `warden-control`，不使用演示或 Mock 数据。认证使用 HttpOnly 会话 Cookie，浏览器存储不保存 JWT。

**权限门控**：`/auth/me` 下发 `capabilities`；`src/shared/api/` 的 `canUser(user, scope)` 是唯一判断入口（旧会话回退到 `scopes`）。`AppSidebar.tsx` 的每个菜单项用 `requires` 声明所需 scope，`App.tsx` 的路由守卫与顶栏也一律通过 `canUser()` 判断，**不要按 `role` 或 `is_platform_admin` 字符串硬编码**。名单与后端 `RequireScope` 守卫共用同一批字符串，定义在 `server/internal/control/authorization.go`。

Connector 页面支持三种接入方式：Operator 配对向导（安装 Helm Chart、输入短期配对码、确认 Namespace 权限范围并等待 Connector 就绪）、Linux 主机配对（输入配对码、选择可管理的 systemd 服务），以及旧版清单 onboarding（兼容）。Connector 列表还支持健康检查、Namespace/systemd 服务范围热更新、安全下线和归档；动作抽屉可按执行环境、范围和资源选择 Linux 主机、systemd 服务与 Docker 容器只读动作。

## 命令

从仓库根目录执行：

```bash
make web-install
make web-dev
make web-build
```

或在本目录执行：

```bash
npm install
npm run dev
npm run build
```

真实 API 接入时按业务 Feature 拆分，避免继续扩大 `app/App.tsx`。

## 补充阅读

- 本地启动与代理端口：[`../docs/quickstart.md`](../docs/quickstart.md)、[`../docs/reference.md#configuration`](../docs/reference.md#configuration)
- SSO 登录入口契约：[`../docs/guides.md#oidc`](../docs/guides.md#oidc)
- 前端部署（静态产物 + SPA fallback）：[`../docs/guides.md#installation`](../docs/guides.md#installation)
- 贡献与验证命令：[`../CONTRIBUTING.md`](../CONTRIBUTING.md)
