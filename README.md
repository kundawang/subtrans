# subtrans

***使用 OpenAI API 翻译字幕文件（SRT / VTT / ASS / LRC / SBV）***

![Go Version](https://img.shields.io/badge/go-1.25%2B-blue)
[![Go Report Card](https://goreportcard.com/badge/github.com/heartleo/subtrans)](https://goreportcard.com/report/github.com/heartleo/subtrans)
[![CI](https://img.shields.io/github/actions/workflow/status/heartleo/subtrans/release.yml)](https://github.com/heartleo/subtrans/actions)
[![Release](https://img.shields.io/github/v/release/heartleo/subtrans)](https://github.com/heartleo/subtrans/releases)
[![Downloads](https://img.shields.io/github/downloads/heartleo/subtrans/total)](https://github.com/heartleo/subtrans/releases)
![License](https://img.shields.io/badge/license-MIT-green)

[中文](README.md) | [English](README_en.md)

## 特性

- 支持多种字幕格式：**SRT**、**WebVTT**、**ASS/SSA**、**LRC**、**SBV**
- 支持 OpenAI 兼容 API
- 支持按句子边界智能分批
- 支持翻译缺失/合并自动重试
- 支持自定义翻译指令和提示词
- 支持原文与译文双语输出
- 支持作为 **命令行工具**、**Go 库** 使用
- 支持 HTTP API 及 SSE 流式输出

## 安装

**Homebrew** (macOS / Linux):

```bash
brew install heartleo/tap/subtrans
```

<!-- **winget** (Windows):

```powershell
winget install heartleo.subtrans
```
-->

**curl** (macOS / Linux):

```bash
curl -fsSL https://raw.githubusercontent.com/heartleo/subtrans/main/install.sh | sh
```

**Go install** (需要 Go 1.25+):

```bash
go install github.com/heartleo/subtrans/cmd/subtrans@latest
```

**从源码编译:**

```bash
git clone https://github.com/heartleo/subtrans
cd subtrans
go build -o subtrans ./cmd/subtrans
```

## 快速开始

### 命令行

```bash
# 创建env文件或设置环境变量
cat > .env <<EOF
OPENAI_API_KEY=sk-xxx
OPENAI_BASE_URL=https://api.openai.com/v1
OPENAI_MODEL=gpt-4.1
EOF

# 默认翻译为中文（按后缀自动识别格式）
subtrans input.srt
subtrans input.vtt
subtrans lyrics.lrc
subtrans anime.ass

# 翻译为法语并指定输出文件
subtrans -l fr -o output.fr.srt input.srt

# 使用自定义 API 地址和模型
export OPENAI_BASE_URL=https://your-api.com/v1
export OPENAI_MODEL=gpt-5.2
subtrans -l fr input.srt
```

### Go 库

```bash
go get github.com/heartleo/subtrans
```

```go
package main

import (
	"context"
	"log"
	"os"

	"github.com/heartleo/subtrans"
)

func main() {
	t, err := subtrans.New(subtrans.Config{
		APIKey: os.Getenv("OPENAI_API_KEY"),
	})
	if err != nil {
		log.Fatal(err)
	}

	subContent, err := os.ReadFile("input.srt")
	if err != nil {
		log.Fatal(err)
	}

	result, err := t.Translate(context.TODO(), string(subContent), "zh")
	if err != nil {
		log.Fatal(err)
	}
}
```

非 SRT 格式通过 `WithFormat` 指定：

```go
t.Translate(ctx, content, "zh", subtrans.WithFormat(subtrans.FormatVTT))
t.Translate(ctx, content, "zh", subtrans.WithFormat(subtrans.FormatASS))

// 或按文件名自动识别
f, _ := subtrans.DetectFormat("input.lrc")
t.Translate(ctx, content, "zh", subtrans.WithFormat(f))
```

### 术语表（固定专有名词译法）

机器翻译常把角色名、地名当成普通词，且同一人物前后译法不一致。术语表可把源词
固定映射到目标词，翻译前后自动保护与恢复：

```go
result, err := t.Translate(ctx, content, "zh", subtrans.WithGlossary(
	subtrans.GlossaryEntry{Source: "Aria", Target: "阿莉娅"},
	subtrans.GlossaryEntry{Source: "Storm King", Target: "风暴之王"},
))

// 也可从术语表文件解析（JSON 或 "源词=目标词" 行）
entries, _ := subtrans.ParseGlossary(data)
t.Translate(ctx, content, "zh", subtrans.WithGlossary(entries...))

// 可选：大小写不敏感匹配（Unicode simple fold）
t.Translate(ctx, content, "zh", subtrans.WithGlossary(entries...),
	subtrans.WithGlossaryCaseInsensitive(true))
```

匹配规则：

- 按字面子串匹配、互不重叠；每个位置**最长词优先**。同时定义 `Storm` 与
  `Storm King` 时，`Storm King` 整体匹配，匹配后从词尾继续扫描。
- 默认**区分大小写**；`WithGlossaryCaseInsensitive` 开启 Unicode 大小写折叠，
  此时仅大小写不同的词条视为重复。
- 每批次用随机占位符替换术语，翻译后再恢复。字幕时间、ASS 样式与 `{\b1}` 等
  覆盖块、VTT 头部及所有未命中文本均保持不变；不传术语表时翻译结果逐字节不变。

校验与回退：

- 源词/目标词为空、包含换行，或同一源词映射到不同目标词 → 在**调用 API 前**
  返回 `ErrInvalidGlossary`；完全相同的重复词条自动去重。
- 重叠词条按最长匹配规则确定，不报错。
- 字幕原文若已包含保留占位符语法，返回 `ErrGlossaryPlaceholder`，而不是当作
  占位符信任。
- 译文丢失、重复或凭空出现占位符时，该行会重试；重试耗尽后 `Translate` 返回
  同时包装 `ErrTranslationIncomplete` 与 `ErrGlossaryPlaceholder` 的错误。

**命令行**：`--glossary terms.json` 指定术语表文件（JSON 对象、
`{"source":"target"}` 数组或 `源词=目标词` 行，支持 `#` 注释）；
`--glossary-case-insensitive` 开启大小写折叠。**服务端**：使用同名 `glossary`
表单字段，可选 `glossary_case_insensitive=true`。

## 支持的字幕格式

| 格式 | 扩展名        | 支持 |
| ---- | ------------- | ---- |
| SRT  | `.srt`        | ✅    |
| VTT  | `.vtt`        | ✅    |
| ASS  | `.ass`/`.ssa` | ✅    |
| LRC  | `.lrc`        | ✅    |
| SBV  | `.sbv`        | ✅    |

## 环境变量

| 变量                 | 说明         | 默认值                      |
| -------------------- | ------------ | --------------------------- |
| `OPENAI_API_KEY`     | API 密钥     | -                           |
| `OPENAI_BASE_URL`    | API 地址     | `https://api.openai.com/v1` |
| `OPENAI_MODEL`       | 模型名称     | `gpt-5.5`                   |
| `OPENAI_TEMPERATURE` | 温度         | `0.0`                       |
| `OPENAI_MAX_RETRIES` | 最大重试次数 | `3`                         |

## Server API

```bash
# 启动服务
subtrans serve

# 自定义提示词
curl -X POST http://localhost:8091/translate \
  -F "file=@input.srt" \
  -F "language=fr" \
  -F "prompt=your-prompt"

# 上传 VTT/ASS/LRC/SBV 字幕文件
curl -X POST http://localhost:8091/translate \
  -F "file=@anime.ass" \
  -F "language=zh"

# 显式指定格式
curl -X POST http://localhost:8091/translate \
  -F "file=@noext_file" \
  -F "format=vtt" \
  -F "language=zh"

# SSE 流式输出，逐批返回翻译结果
curl -X POST http://localhost:8091/translate \
  -H "Accept: text/event-stream" \
  -F "file=@input.srt" \
  -F "language=zh"
```

| 参数           | 说明                                      | 默认值     |
| -------------- | ----------------------------------------- | ---------- |
| `file`         | 字幕文件                                  | -          |
| `format`       | 字幕格式（`srt`/`vtt`/`ass`/`lrc`/`sbv`） | 按后缀识别 |
| `language`     | 目标语言 ISO 代码                         | `zh`       |
| `prompt`       | 自定义提示词                              | -          |
| `instructions` | 自定义系统指令                            | -          |
| `glossary`     | 术语表（JSON 或 `源词=目标词` 行）        | -          |
| `glossary_case_insensitive` | 术语表大小写不敏感匹配        | `false`    |

## 命令行参数

全局参数（翻译和 serve 均支持）：

| 参数        | 缩写 | 说明         | 默认值  |
| ----------- | ---- | ------------ | ------- |
| `--verbose` | `-v` | 启用调试日志 | `false` |

### subtrans

| 参数                  | 缩写 | 说明                                              | 默认值                       |
| --------------------- | ---- | ------------------------------------------------- | ---------------------------- |
| `--language`          | `-l` | 目标语言 ISO 代码                                 | `zh`                         |
| `--output`            | `-o` | 输出文件路径                                      | `<输入文件>.<语言>.<原后缀>` |
| `--model`             | `-m` | 模型                                              | -                            |
| `--max-batch-size`    |      | 每批行数                                          | `30`                         |
| `--batch-split-punct` |      | 分批切分标点符号                                  | `.`                          |
| `--instructions`      |      | 指令文本文件路径                                  | -                            |
| `--prompt`            |      | 自定义用户提示词                                  | -                            |
| `--glossary`          |      | 术语表文件路径（JSON 或 `源词=目标词` 行）        | -                            |
| `--glossary-case-insensitive` | | 术语表大小写不敏感匹配                     | `false`                      |
| `--temperature`       |      | 温度                                              | `0.0`                        |
| `--max-retries`       |      | API 失败重试次数                                  | `3`                          |
| `--include-original`  |      | 输出中包含原文                                    | `false`                      |
| `--strip-punctuation` |      | 去除译文及原文（`--include-original` 时）尾部标点 | `true`                       |

### subtrans serve

| 参数     | 缩写 | 说明     | 默认值      |
| -------- | ---- | -------- | ----------- |
| `--host` |      | 监听地址 | `localhost` |
| `--port` | `-p` | 监听端口 | `8091`      |

---

<div align="center">

Made with ❤️ by [heartleo](https://github.com/heartleo)

</div>
