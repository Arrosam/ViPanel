# 安装 ViPanel

## 这套东西会以 root 跑在你的机器上

先读 [trust-model.md](trust-model.md)。一句话：**能打开 ViPanel 控制台的人，
约等于能在这台机器上以 root 执行任意命令。** 装之前想清楚谁会有这个入口。

## 前提

- Linux，amd64 或 arm64
- root
- 目标机器上装好 `claude`：`npm i -g @anthropic-ai/claude-code`
  （agent 直接拉起宿主机上的 `claude`，不进容器 —— 进了容器它就管不了这台机器）

## 一键装（推荐）

```bash
curl -fsSL https://raw.githubusercontent.com/Arrosam/ViPanel/main/scripts/get.sh | sudo sh
```

它会探测架构、下载对应的发布产物、**比对 sha256**、解压，然后调 `install.sh`。
校验和不过就中止——这个脚本要把四个以 root 运行的二进制放进 `/usr/local/bin`，
不比对就等于「网络上给什么就装什么」。

不想走管道的话，从 [Releases](https://github.com/Arrosam/ViPanel/releases)
下对应架构的 `.tar.gz`，自己核一遍 `SHA256SUMS`，解压后：

```bash
sudo sh scripts/install.sh
```

**下载量**：每个架构的 tar.gz 约 102MB（解压后 362MB，其中 `1panel-core`
自己就占 277MB —— 前端整个 embed 在里面）。这是上游的形态，我们没动。

**只支持单节点。** 控制台和 MCP 只在主节点上可用——它们走
`/etc/1panel/agent.sock`，而那个 socket 只在主节点模式下创建。
被纳管的从节点上控制台能打开但每次工具调用都会退回终端确认，MCP 连不上。

**验证过的架构**：arm64（Debian 12 上做的全部真机验收，含从零安装）。
amd64 能交叉编译并随发布产出，但**没有在真机上跑过** —— 用它请自己先验一遍。

---

## 自己构建（开发用）

发布产物已经覆盖两种架构，下面这套只在改代码时需要。

在开发机上：

```bash
cd frontend && npx vite build --mode production   # 产物 embed 进 core
mkdir -p build
cd core  && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../build/1panel-core  ./cmd/server
cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../build/1panel-agent ./cmd/server
cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../build/vipanel-hook ./cmd/hook
cd agent && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../build/vipanel-mcp ./cmd/mcp
```

`GOARCH` 按目标机器改（x86_64 用 `amd64`）。发布流水线还会加
`-ldflags '-s -w'`，但**别指望它解决体积**：实测只省 5%（core 292MB → 277MB），
大头是 embed 进去的前端和一大堆 Go 依赖，不是调试信息。

**前端必须用 `--mode production`。** `development` 模式的产物里有 Node 内置模块被
externalize，入口 chunk 求值时抛错、`app.mount()` 永远不执行，页面停在转圈 ——
而且**控制台一条错误都没有**（模块求值失败不进 console）。这个坑很难查。

把 `build/` 和 `scripts/install.sh` 拷到目标机器，然后：

```bash
sudo ./install.sh
```

不带参数时会随机生成密码并打印出来。想指定：

```bash
sudo PORT=9999 USERNAME=admin PASSWORD=你的密码 ./install.sh
```

脚本可重复执行：已存在的配置不会被冲掉。

## 三、验证

```bash
systemctl status vipanel-agent vipanel-core
curl -s -o /dev/null -w '%{http_code}\n' http://127.0.0.1:9999/
```

浏览器打开 `http://<机器 IP>:9999`，用上面的账号登录，进 **AI → 控制台**。

## 四、Agent 登录

首次进控制台会看到「Agent 未登录」横幅。点「去登录」→ 选订阅或 Console →
**在你自己的设备上**打开面板给出的链接（服务器上不会弹浏览器，
`BROWSER=/usr/bin/true` 就是为了阻止它）→ 把授权码贴回来。

登录完成后**不需要任何额外配置**：首次运行向导、目录信任、一次性提示
都由 harness 在起进程前预置好了。

## 五、升级

```bash
systemctl stop vipanel-core vipanel-agent
# 覆盖 /usr/local/bin/{1panel-core,1panel-agent,vipanel-hook,vipanel-mcp}
systemctl start vipanel-agent vipanel-core
```

配置和数据库都在 `$BASE_DIR/1panel` 下，不会被覆盖。

## 常见问题

**页面一直转圈** — 前端用了 `development` 模式构建，见上面。

**改完 agent 只重启 agent，结果面板打不开** — `vipanel-core.service` 里写着
`Requires=vipanel-agent.service`，而 systemd 的 `Requires` 会**传播停止**：
`systemctl stop vipanel-agent` 会把 core 一起收走，`Restart=always` 对
「被正常停止」不生效，所以它不会自己回来。热更 agent 时要么两个一起重启：

```bash
systemctl restart vipanel-agent vipanel-core
```

要么停完 agent 之后补一句 `systemctl start vipanel-core`。
**注意所有走 unix socket 的自测都绕过 core，看不出这个问题**——
判断面板是否真的活着要看 `curl http://127.0.0.1:9999/` 的状态码，
不能只看 `systemctl is-active vipanel-agent`。

**设置里「面板操作能力」灰着点不动** — `/usr/local/bin/vipanel-mcp` 不存在或没有
执行权限。它必须和 `1panel-agent` 同目录：面板按自己二进制的所在目录去找它。
没有它，agent 仍能正常对话和操作这台机器，只是不能操作面板本身。

**控制台顶部红色「权限代理未生效」** — `/usr/local/bin/vipanel-hook` 不存在或没有
执行位。这时工具调用不经过面板确认，agent 会直接以当前权限执行。

**聊天区一直是空的，但终端里 agent 明明在回话** — 检查启动 core/agent 的那个 shell
里有没有 `CLAUDE_CODE_*` 环境变量。claude 检测到自己是另一个 claude 会话的子进程时
会关掉 transcript 写入，而聊天完全建立在 transcript 上。ViPanel 起 agent 时会剔除
这些变量，但如果是更外层的问题（比如整个 systemd 环境里带着），就要自己清。

**访问 `/xxx` 返回 "Access Temporarily Unavailable"** — 那个路径不在
`core/constant/common.go` 的 `WebUrlMap` 白名单里。这个提示词有误导性，
它跟安全入口和登录态都没关系，就是路径不认识。
