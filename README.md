# zhihu-cli Go

一个参考 [BAIGUANGMEI/zhihu-cli](https://github.com/BAIGUANGMEI/zhihu-cli) 翻译的纯 Go 版知乎命令行工具。

## 状态

- 使用 Go 标准库实现，无 Python 运行时依赖。
- 支持 Cookie 登录、扫码登录轮询、状态检查、退出、个人信息、搜索、热榜、问题/回答/评论、推荐流、话题、用户资料、关注者、收藏夹、通知、点赞/取消、关注问题、发布提问/想法/文章、图片上传和删除自己发布的内容。
- Cookie 只保存在本机 `~/.zhihu-cli/cookies.json`，文件权限为 `0600`。
- `login --qrcode` 会输出知乎扫码链接，并保存到 `~/.zhihu-cli/login_qrcode.txt`。为了保持标准库实现，没有内置二维码 PNG/终端二维码生成。
- 按本仓库约定，缺少必需 Cookie、配置损坏或 API 失败会直接报错，不做静默 fallback。

## 安装与运行

```bash
go run ./cmd/zhihu --help
go run ./cmd/zhihu login --cookie "z_c0=...; _xsrf=...; d_c0=..."
go run ./cmd/zhihu status
go run ./cmd/zhihu whoami --json
```

构建二进制：

```bash
go build -o zhihu ./cmd/zhihu
```

## 常用命令

```bash
zhihu search "Go 语言" --limit 5
zhihu hot --limit 20
zhihu notifications --monitor
zhihu question 123456
zhihu answers 123456 --limit 5
zhihu answer 789 --comments --limit 10
zhihu user url-token
zhihu user-answers url-token
zhihu vote 789
zhihu follow-question 123456
zhihu reply-comment 789 "回复内容" --resource-type pin --resource-id 123456
zhihu ask "问题标题" -d "问题描述" -t 100
zhihu pin "想法标题" -c "正文"
zhihu article "文章标题" "正文"
```

通知命令会显示通知发起人的粉丝数、关注关系，以及被赞同的回答/文章/想法当前赞同数。监控模式会先展示最新 10 条，然后每 60 秒刷新一次，只输出新增通知。想看更多初始通知可以加 `-l`：

```bash
zhihu notifications --monitor
zhihu notifications --monitor -l 50
zhihu notifications --monitor --interval 30
```

删除命令必须显式传 `-y`：

```bash
zhihu delete-question 123456 -y
zhihu delete-pin 123456 -y
zhihu delete-article 123456 -y
zhihu delete-comment 123456 -y
```

## 测试

```bash
go test ./...
```

## 安全审查说明

翻译前已检查参考仓库的核心源码、依赖声明、命令入口和测试。未发现安装钩子、执行任意命令、读取本机敏感文件或将 Cookie 发往非知乎服务的疑似恶意代码。参考实现会访问知乎 API、知乎图片上传 OSS、GitHub/PyPI 元数据链接，这些与项目功能和依赖声明相符。
