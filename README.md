# zhihu-tui

一个用 Go 编写的知乎命令行工具，重点提供适合长文阅读和评论互动的终端 TUI。

> [!IMPORTANT]
> 本项目是非官方客户端，与知乎无隶属或合作关系。请遵守知乎的服务条款及所在地法律法规，不要将本项目用于绕过访问控制、批量抓取或其他滥用行为。

## 功能

- 全屏关注流 TUI：连续阅读回答、问题、文章和想法，支持评论、点赞与浏览器打开原文。
- 内容浏览：搜索、热榜、问题、回答、评论、推荐流、话题和用户主页。
- 互动与创作：点赞、关注问题、回复评论、发布提问/想法/文章、上传图片和删除自己的内容。
- 通知：浏览、标记已读及终端常驻监控。
- 认证：支持 Cookie 登录和二维码登录轮询。
- 纯 Go 实现：除 `github.com/rivo/uniseg` 外不依赖第三方运行时。

## 安装

需要 Go 1.25 或更高版本。

```bash
go install github.com/JimChengLin/zhihu-tui/cmd/zhihu@latest
```

也可以从源码构建：

```bash
git clone https://github.com/JimChengLin/zhihu-tui.git
cd zhihu-tui
go build -o zhihu ./cmd/zhihu
```

## 快速开始

```bash
# 二维码登录；扫码链接同时保存到 ~/.zhihu-cli/login_qrcode.txt
zhihu login --qrcode

# 或手动保存 Cookie（至少包含 z_c0、_xsrf、d_c0）
zhihu login --cookie "z_c0=...; _xsrf=...; d_c0=..."

# 检查登录状态并进入关注流 TUI
zhihu status
zhihu feed --tui
```

Cookie 仅保存在本机 `~/.zhihu-cli/cookies.json`，权限为 `0600`。本项目不会将 Cookie 上传给项目维护者或第三方服务。

## 常用命令

```bash
zhihu search "Go 语言" --limit 5
zhihu hot --limit 20
zhihu question 123456
zhihu answers 123456 --limit 5
zhihu answer 789 --comments --limit 10
zhihu feed --tui
zhihu user url-token
zhihu user-answers url-token
zhihu notifications --monitor
zhihu notifications mark-read --all-tabs
zhihu vote 789
zhihu follow-question 123456
zhihu reply-comment 789 "回复内容" --resource-type pin --resource-id 123456
zhihu ask "问题标题" -d "问题描述" -t 100
zhihu pin "想法标题" -c "正文"
zhihu article "文章标题" "正文"
```

查看完整命令列表及参数：

```bash
zhihu --help
zhihu <command> --help
```

删除命令必须显式传入 `-y`：

```bash
zhihu delete-question 123456 -y
zhihu delete-pin 123456 -y
zhihu delete-article 123456 -y
zhihu delete-comment 123456 -y
```

## 关注流 TUI

登录后运行：

```bash
zhihu feed --tui
```

该模式读取关注流并用全屏终端界面展示内容。接近已加载内容末尾时会自动预取下一页；终端宽度达到 120 列时自动切换为列表与正文双栏，缩回窄窗口后恢复单栏。

| 按键 | 操作 |
| --- | --- |
| `j` / `k`、方向键 | 向下 / 向上滚动正文 |
| `f` / `b`、`Space` / `Ctrl-F`、`Ctrl-B` | 向下 / 向上翻页 |
| `d` / `u` | 向下 / 向上翻半页 |
| `Ctrl-E` / `Ctrl-Y` | 向下 / 向上滚动一行 |
| `n` / `p`、`h` / `l`、左右方向键 | 切换动态 |
| `g` / `G` | 跳到第一条 / 最后一条已加载动态 |
| `c` | 在正文与评论区之间切换 |
| `v` | 点赞 / 取消点赞当前回答或焦点评论 |
| `z` | 切换专注阅读模式 |
| `o` | 用默认浏览器打开当前动态 |
| `r` | 刷新关注流 |
| `?` | 显示完整帮助 |
| `q` / `Ctrl-C` | 退出 |

正文中的图片当前显示为 `▣ 图片 N` 占位。

## 通知监控

监控模式默认展示最新 10 条通知，然后每 60 秒刷新一次，仅输出新增通知：

```bash
zhihu notifications --monitor
zhihu notifications --monitor -l 50
zhihu notifications --monitor --interval 30
```

发现新通知并发送终端响铃后，程序会将 `default`、`follow`、`vote_thank` 三个通知页签标记为已读。

## 开发

```bash
go test ./...
```

项目主要目录：

```text
cmd/zhihu/          命令行入口
internal/auth/      Cookie 与二维码登录
internal/client/    知乎 API 客户端
internal/cli/       命令解析与执行
internal/display/   终端文本展示
internal/feedtui/   关注流 TUI
```

## API 来源与致谢

本项目的部分知乎 API 路径、请求参数和认证流程借鉴了 [BAIGUANGMEI/zhihu-cli](https://github.com/BAIGUANGMEI/zhihu-cli)。感谢原项目作者公开实现与研究成果。

参考项目采用 [Apache License 2.0](https://github.com/BAIGUANGMEI/zhihu-cli/blob/main/LICENSE) 发布。本项目在遵守其许可证的前提下进行独立的 Go 实现，并在 [`NOTICE`](NOTICE) 中保留来源与归属说明。

## License

本项目基于 [Apache License 2.0](LICENSE) 发布。
