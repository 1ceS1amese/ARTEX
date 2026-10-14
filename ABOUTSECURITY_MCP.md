# AboutSecurity MCP 接入 ARTEX

## 当前接入方式

`AboutSecurity` 是资料仓库，MCP 服务由独立项目 `context1337` 提供。ARTEX 已有
Streamable HTTP MCP 客户端（`mcphttp/client.go`）、MCP 服务器登记表、工具缓存和
Agent 可见性表。ARTEX 启动时会预置一个**禁用**的 `aboutsecurity` MCP 项；
实际 URL 和请求头仍通过原有 MCP 管理页面配置。`planner` 会获得一次性默认可见性，
已有的同名 MCP 配置不会被覆盖；以后在页面撤销授权，重启也不会重新授予。

## 部署知识库服务

ARTEX 只依赖 `context1337` 提供的标准 Streamable HTTP MCP 地址，不依赖仓库的
本机路径或同级目录布局。按 `context1337` 的部署说明启动服务并构建 AboutSecurity
索引；下面仅是将两个仓库放在同级目录时的本地开发示例。
源码构建需要 Go 1.25+、C 编译器、Python 3、`jieba` 和 `pyyaml`。

```sh
cd ../context1337
python3 -m venv /tmp/context1337-index-venv
. /tmp/context1337-index-venv/bin/activate
make run
```

`make run` 会读取同级的 `AboutSecurity`，建索引并前台启动 `:1337`；以后已有索引时
可用 `./absec serve --port 1337 --data-dir ./data` 启动。健康检查是
`http://127.0.0.1:1337/health`，MCP 地址是 `http://127.0.0.1:1337/mcp`。
首次索引后建议保留 `data/builtin.db`，不要在每次启动时重建。

服务用于多人或可被其他机器访问时，应按 `context1337` 的密钥说明配置仅有
`builtin` 来源和 `read` 权限的密钥。不要把密钥写进命令参数、文档或 Git。
ARTEX 现有 MCP 配置会将 HTTP 请求头保存在 PostgreSQL 的
`mcp_servers.env`，应限制数据库与 ARTEX 管理页面访问权限。

## 登记到 ARTEX

启动 ARTEX 后，在「系统配置 → MCP」打开预置的 `aboutsecurity` 项：

1. 传输方式保持 `http`，URL 填写 `context1337` 的 `/mcp` 地址。
2. 服务端启用了密钥时，在请求头中填写 `Authorization=Bearer <只读密钥>`。
3. 保存并启用，刷新工具列表，确认出现 `search_security`、
   `get_security_detail`、`read_security_file`（默认 lite 模式）。
4. `planner` 在首次启动或升级时已默认获得可见性。需要让 `worker`、`pentest` 等
   其他 Agent 查询知识库时，在该 MCP 项的 Agent 可见性中授权；也可在 Agent 管理页调整。

若 ARTEX 在 Docker 容器中，`127.0.0.1` 指向容器自身；应使用容器能够访问的
`context1337` 地址，如同一 Docker 网络内的 `http://context1337:1337/mcp`。
修改绑定后，新启动的 Agent 运行会加载新工具；已运行会话需要重启。

## 兼容性测试

先用可丢弃的 PostgreSQL 数据库验证一次性授权与撤销后的重启行为：

```sh
ARTEX_ABOUTSECURITY_TEST_DSN='postgres://.../disposable-db' \
  go test ./db -run TestAboutSecurityPlannerVisibility -count=1 -v
ARTEX_ABOUTSECURITY_TEST_DSN='postgres://.../disposable-db' \
  go test ./server -run TestAboutSecurityPlannerNativeAssembly -count=1 -v
```

知识库服务和 ARTEX 均启动后，可再验证真实 MCP 协议和 `planner` 工具装配：

```sh
export ABOUTSECURITY_MCP_TEST_URL=http://127.0.0.1:1337/mcp
# 服务配置了密钥时，在当前 shell 中设置 ABOUTSECURITY_MCP_TEST_AUTHORIZATION
export ARTEX_PG_DSN='<ARTEX 当前使用的 PostgreSQL DSN>'
go test ./mcphttp -run TestContext1337Compatibility -count=1 -v
go test ./server -run TestAboutSecurityAgentToolAssembly -count=1 -v
```

独立数据库测试检查默认授权、旧库升级和撤销后不恢复；内建 MCP 测试用进程内服务
检查 `planner` 的工具装配与调用，不依赖本机知识库。真实服务测试检查初始化、
`tools/list`、Skill 检索与详情、字典检索与按行读取，并验证已登记的服务能被
`planner` 装配。
未设置 `ABOUTSECURITY_MCP_TEST_URL` 时，该真实服务测试会跳过，普通单元测试仍可运行。
可在 ARTEX 管理页面的 MCP 列表检查 3 个缓存工具及 Agent 可见性；若服务改用
`full` 工具模式，需在页面刷新工具列表并调整此测试的预期。

## 回滚

在 ARTEX 管理页面取消各 Agent（包括 `planner`）对 `aboutsecurity` 的可见性并禁用该 MCP 项。
停止 `context1337` 服务即可停止查询。若删除预置项，ARTEX 下次启动会重新创建
一个禁用占位项，不会恢复已删除的授权。
本接入没有修改 AboutSecurity 原始资料或 ARTEX 的 Agent 主循环。
