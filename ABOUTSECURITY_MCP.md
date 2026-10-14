# AboutSecurity MCP 接入 ARTEX

## 当前接入方式

`AboutSecurity` 是资料仓库，MCP 服务由独立项目 `context1337` 提供。ARTEX 已有
Streamable HTTP MCP 客户端（`mcphttp/client.go`）、MCP 服务器登记表、工具缓存和
Agent 可见性表。ARTEX 启动时会预置一个**禁用**的 `aboutsecurity` MCP 项；
实际 URL、请求头和 Agent 授权仍通过原有 MCP 管理页面配置。已有的同名配置不会被覆盖。

## 本地启动知识库服务

在 Linux/WSL 中，确保 `AboutSecurity`、`ARTEX`、`context1337` 是同级目录。
`context1337` 需要 Go 1.25+、C 编译器、Python 3、`jieba` 和 `pyyaml`。

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
4. 在该 MCP 项的 Agent 可见性中授予需要查询知识库的 Agent，例如 `worker`、
   `pentest`；也可在 Agent 管理页调整。

若 ARTEX 在 Docker 容器中，`127.0.0.1` 指向容器自身；应使用容器能够访问的
`context1337` 地址，如同一 Docker 网络内的 `http://context1337:1337/mcp`。
修改绑定后，新启动的 Agent 运行会加载新工具；已运行会话需要重启。

## 兼容性测试

知识库服务和 ARTEX 均启动后，在 ARTEX 目录运行：

```sh
export ABOUTSECURITY_MCP_TEST_URL=http://127.0.0.1:1337/mcp
# 服务配置了密钥时，在当前 shell 中设置 ABOUTSECURITY_MCP_TEST_AUTHORIZATION
go test ./mcphttp -run TestContext1337Compatibility -count=1 -v
ARTEX_CONFIG="$PWD/config.json" go test ./server -run TestAboutSecurityAgentToolAssembly -count=1 -v
```

测试检查初始化、`tools/list`、Skill 检索与详情、字典检索与按行读取，
并从 ARTEX 已登记的数据库配置验证 `worker` 能装配检索工具。
未设置 `ABOUTSECURITY_MCP_TEST_URL` 时，该真实服务测试会跳过，普通单元测试仍可运行。
可在 ARTEX 管理页面的 MCP 列表检查 3 个缓存工具及 Agent 可见性；若服务改用
`full` 工具模式，需在页面刷新工具列表并调整此测试的预期。

## 回滚

在 ARTEX 管理页面取消各 Agent 对 `aboutsecurity` 的可见性并禁用该 MCP 项。
停止 `context1337` 服务即可停止查询。若删除预置项，ARTEX 下次启动会重新创建
一个禁用占位项，不会恢复已删除的授权。
本接入没有修改 AboutSecurity 原始资料或 ARTEX 的 Agent 主循环。
