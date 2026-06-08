# sshmenu

从 `~/.ssh/config` 读取 SSH 主机列表，用终端交互界面选择并连接。

## 构建

```bash
make build      # 当前平台
make linux      # Linux amd64
make windows    # Windows amd64 (.exe)
```

## 使用

```bash
./sshmenu
./sshmenu --version   # 查看版本
```

## 展示

![sshmenu](assert/image.png)

## 操作

| 按键 | 功能 |
|---|---|
| j/k/↑/↓ | 移动光标 |
| 输入字符 | 实时过滤 |
| Backspace | 删除过滤字符 |
| Esc | 清除过滤 / 退出 |
| Enter | 连接选中主机 |
| q / Ctrl+C | 退出 |

## 最近使用排序

sshmenu 会记录你连接过的主机（LRU 最近最久未使用），下次启动时常用的主机会排在列表前面。

历史记录存储在：
- Linux: `~/.config/sshmenu/last`
- Windows: `%APPDATA%\sshmenu\last`

## 项目结构

```
cmd/sshmenu/
├── main.go           # 入口
├── types.go          # 类型定义
├── config.go         # SSH config 解析
├── key.go            # 键盘输入
├── ssh.go            # SSH 连接
├── tui.go            # 终端界面
├── history.go        # 最近使用记录
└── sshmenu_test.go   # 测试
```

## 依赖

- `golang.org/x/term`

## 发布

推送版本 tag 即可自动构建并发布到 GitHub Releases：

```bash
git tag v1.0.0
git push origin v1.0.0
```

GitHub Actions 会自动编译 linux/windows 版本并上传到 Release 页面。

## 协议

MIT License - 详见 [LICENSE](LICENSE)

© LingNc
