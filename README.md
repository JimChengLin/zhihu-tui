# zhihu-cli Go

一个参考 [BAIGUANGMEI/zhihu-cli](https://github.com/BAIGUANGMEI/zhihu-cli) 翻译的纯 Go 版知乎命令行工具。

## 状态

- 使用 Go 标准库实现，无 Python 运行时依赖。
- 支持 Cookie 登录、扫码登录轮询、状态检查、退出、个人信息、搜索、热榜、问题/回答/评论、推荐流、关注页 TUI、话题、用户资料、关注者、收藏夹、通知、点赞/取消、关注问题、发布提问/想法/文章、图片上传和删除自己发布的内容。
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
zhihu feed --tui
zhihu notifications --monitor
zhihu notifications mark-read --all-tabs
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

### 关注页 TUI

登录后运行：

```bash
zhihu feed --tui
```

该模式读取 `/api/v3/moments` 关注流，使用全屏终端界面展示回答、问题、文章和想法。正文中的图片会显示为 `▣ 图片 N` 占位，当前动态支持评论时可直接切进评论区。接近已加载内容末尾时会自动预取下一页。终端达到 120 列时会自动切换为动态列表与正文双栏，正文会随可用空间在 96–112 cell 之间适度伸缩，缩回窄窗口后恢复单栏，窗口尺寸变化时会实时重排。

- `j` / `k` 或方向键：滚动正文；到达正文边缘后切换动态
- `space` / `b`：向下 / 向上翻页
- `n` / `p`、`h` / `l` 或左右方向键：直接切换动态
- `g` / `G`：跳到第一条 / 最后一条已加载动态
- `c`：在当前动态的正文与评论区之间切换
- `z`：切换隐藏动态列表的专注阅读模式
- `o`：用默认浏览器打开当前动态
- `r`：刷新关注流，并在完成后标记 `NEW` 内容和上次列表的首尾
- `?`：查看完整帮助
- `q` 或 `Ctrl-C`：退出

通知命令会显示通知发起人的粉丝数、关注关系，以及被赞同的回答/文章/想法当前赞同数。监控模式会先展示最新 10 条，然后每 60 秒刷新一次，只输出新增通知。想看更多初始通知可以加 `-l`：

```bash
zhihu notifications --monitor
zhihu notifications --monitor -l 50
zhihu notifications --monitor --interval 30
zhihu notifications mark-read --tab default
zhihu notifications mark-read --all-tabs
```

监控模式发现新通知并发送终端响铃后，会自动执行等价于 `zhihu notifications mark-read --all-tabs` 的操作，把 `default`、`follow`、`vote_thank` 三个通知页签标记为已读。

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
