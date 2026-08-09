# ViPanel

**ViPanel 是 [1Panel](https://github.com/1Panel-dev/1Panel) 的一个小规模改动版本。**

修改者：Arrosam · 最近修改日期：2026-08-07 · 上游基线：`dev-v2`

本项目与飞致云 / FIT2CLOUD 及 1Panel 官方**没有任何隶属关系**，也未获其背书。
遵循上游的 GPL-3.0 协议。

---

## 改了多少

| | |
|---|---:|
| 上游文件总数 | 1704 |
| **被改动的上游文件** | **19（1.1%）** |
| 从上游删掉的行 | **2** |
| 新增文件 | 56 |

改动之所以能这么小，是刻意的：新功能几乎全部落在新文件里，
碰上游文件只为了「登记」——加一条路由、加一条菜单、加一条迁移。
这样每次 `git rebase upstream/dev-v2` 的冲突面才能控制住
（最近一次演练：上游 6 个新提交，只冲突 1 个文件）。

被改动的 19 个上游文件，按性质分：

- `frontend/src/lang/modules/*.ts`（12 个）—— 加控制台的界面文案
- `frontend/src/routers/modules/ai.ts` —— 加一条路由
- `core/constant/common.go` —— 把新路径加进 SPA 白名单
- `core/init/migration/migrate.go`、`agent/init/migration/migrate.go` —— 各加一条菜单迁移
- `agent/router/ro_ai.go` —— 加控制台的接口
- `agent/init/business/business.go` —— 启动时恢复会话
- `.gitignore`

## 加了什么

只有一样东西：**AI 菜单下的「控制台」**。

### 会话面板

把 agent（目前是 Claude Code）**直接跑在宿主机上**，作为 `1panel-agent` 的子进程，
而不是关进容器。这是和上游 Agents 功能最根本的分歧：进了容器的 agent 看不见宿主机的
文件系统、systemd、docker 和网络，那不是管服务器，是管一个容器。

代价写在 [docs/trust-model.md](docs/trust-model.md) 里，一句话：
**能打开 ViPanel 控制台的人，约等于能在这台机器上以 root 执行任意命令。**

界面上是一个三栏的会话面板：会话列表、结构化聊天、文件管理 + 终端。
聊天不是解析终端画面得来的——agent 的对话记录本身是结构化的，我们读那一份，
终端那一路只用来跑真正的 TUI。

### 权限代理

没有沙箱兜底，所以每次工具调用的确认是唯一的边界。agent 每调一次工具都经过
一个钩子，钩子阻塞等浏览器上的人做决定。联系不上面板时降级为「退回终端确认」，
不是放行。

### 面板操作能力（MCP）

让 agent 能通过面板自己的接口建站、装应用、管容器和数据库——
574 个操作，按 15 个板块渐进式披露。每一次变更都会在浏览器上弹出一句人话
（「删除网站 example.com，同时删除数据库和备份」），而不是一段 JSON。

工具目录不是手写的，是从 1Panel 自己的 swagger 编译出来的，
人工评审面只有一份清单。上游 rebase 后新增的端点默认**不暴露**，
必须有人看过才开。

## 明确不做的

**只做单节点。** 目标是让个人开发者和普通用户在本地把**一台**服务器管好，
不是做多机编排——那是上游商业版的范围。

具体后果：控制台和 MCP 都只在**主节点**上可用。它们走
`/etc/1panel/agent.sock`，而那个 socket 只在 agent 以主节点模式启动时才创建；
被纳管的从节点上 agent 走 HTTPS + 端口，没有这个 socket。

在从节点上会怎样：控制台页面能打开、能聊天，但每次工具调用都会因为钩子连不上面板
而**退回终端确认**（降级方向是安全的，不是放行），MCP 则完全连不上。
换句话说是「不可用」，不是「不安全」。

## 没改什么

上游的全部功能、界面和行为都保持原样：应用商店、网站、容器、数据库、
计划任务、备份、防火墙、监控、工具箱、设置，以及原有的 AI 相关功能
（模型账号、Agents、MCP 托管、GPU、Ollama）。

## 从哪来到哪去

- 上游：https://github.com/1Panel-dev/1Panel
- 协议：GPL-3.0（与上游一致，见 [LICENSE](LICENSE)）
- 安装：[docs/install.md](docs/install.md)
- 信任模型：[docs/trust-model.md](docs/trust-model.md)（**装之前请先读**）
