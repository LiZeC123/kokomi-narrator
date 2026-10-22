# Kokomi Narrator

把长篇文本变成有声书的个人 TTS 工作流工具。

`ttsctl` 负责把文本切成片段、调用 TTS 服务、管理每段的合成状态，并在需要时试听、重做和合并。它不内置 TTS 引擎，而是连接一个返回 WAV 音频的 HTTP 服务。

---

## 为什么做这个

长篇文本的语音合成通常不是一次完成的：文本会反复修改，个别片段可能需要重做，服务也可能失败或中断。如果每次全文重新合成，既慢又浪费。

Kokomi Narrator 把“文本文件”拆成“语音片段”来管理：

- 已经合成过的片段不会重复请求
- 修改文本后只处理真正变化的片段
- 失败的片段可以单独重试
- 听感不对的片段可以直接定位、重做并复听
- 最终把全部片段合并成一个完整的 WAV

它面向个人使用，不是多用户在线服务，也不需要额外的数据库服务。

---

## 主要功能

- **文本导入与分段**：按行解析 `.txt`，自动区分可朗读内容和纯标点
- **增量更新**：修改文本后尽量保留未变化片段的状态和音频
- **内容寻址缓存**：相同音色、相同文本的所有片段共享同一份音频
- **并发合成**：默认同时向 TTS 服务发送 2 个请求，可配置
- **失败重试**：支持超时、重试和退避；单个片段失败不影响其他片段
- **断点续跑**：中断或进程异常退出后，下次执行 `make` 会继续处理未完成片段
- **状态查看**：按文件列出片段、状态、错误和文本
- **文本搜索**：按关键字找到对应的片段序号（seq）
- **试听与返修**：播放某一段音频，或重做后立即试听
- **WAV 合并**：按顺序合并片段，并在片段之间插入可配置的静音
- **EPUB 导入**：从 EPUB 提取章节文本，保留目录结构
- **音频清理**：删除没有任何数据库记录引用的音频文件
- **文件 ID**：用短数字 ID 代替中文文件名，输入更省事

---

## 工作原理

整体流程很直接：

```text
TXT / EPUB
   │
   ▼
按行切分片段
   │
   ▼
SQLite 记录文件与片段状态
   │
   ▼  只选择待处理片段
TTS HTTP 服务（可并发、可重试）
   │
   ▼
校验后的 WAV 音频
   │
   ▼
merge → output/<文件名>.wav
```

几个关键设计：

- **音频内容寻址**：音频路径由“音色标识 + 文本 SHA-256”推导，不把路径存进数据库，因此相同文本可以跨文件复用。
- **片段状态机**：每个片段有 `pending / processing / success / failed / skipped` 状态。`make` 启动时会先根据实际音频文件修正数据库状态。
- **原子写入**：音频先写临时文件，校验通过后再替换目标文件，避免留下半截 WAV。
- **增量更新**：文本变化时按内容哈希匹配旧片段，尽量保留原状态，只调整片段顺序。
- **安全中断**：中断时把正在处理的片段恢复为 `pending`，下次继续。

---

## 快速开始

### 环境要求

- Go 1.25.1 或更高版本
- 一个可用的 TTS HTTP 服务，返回 PCM WAV
- 试听功能需要系统播放器；macOS 自带 `afplay`，Linux 可使用 `ffplay` 或 `mpv`

### 构建

```bash
cd kokomi-narrator
go build -o ttsctl .
```

### 配置

默认读取当前目录的 `config.yaml`，也可以通过 `--config` 指定其他路径。

```yaml
database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: info

tts:
  url: "http://127.0.0.1:8080/tts"
  character: "Kokomi"
  concurrency: 2
  timeout: 60s
  retries: 2
  retry_backoff: 2s
  headers: {}

merge:
  overwrite: true
  gap_between_segments: 200ms
  leading_trailing_gap: false
```

常用配置说明：

- `tts.character`：音色标识，修改后 `make` 会重新合成
- `tts.concurrency`：同时请求 TTS 的数量，默认 2
- `tts.headers`：需要认证时附加的请求头
- `merge.gap_between_segments`：片段之间的静音长度
- `merge.leading_trailing_gap`：是否在整段音频首尾也插入静音

### TTS 接口

项目调用 `tts.url`，请求体为扁平 JSON，文本需要做 URI 编码：

```http
POST /tts
Content-Type: application/json

{
  "character": "Kokomi",
  "text": "%E4%BD%A0%E5%A5%BD"
}
```

服务需要返回 HTTP 200 和合法的 PCM WAV。项目会校验 WAV 的 `fmt`、`data` 和 PCM 格式；不符合要求会重试并记录错误。

---

## 基本流程

```bash
# 1. 导入当前目录下的文本
./ttsctl add novel.txt

# 2. 查看文件 ID
./ttsctl files
# ID  SEGMENTS  FILE
# 1   120       novel.txt

# 3. 合成待处理片段
./ttsctl make 1

# 4. 查看片段状态
./ttsctl ls 1

# 5. 按文字找到片段序号
./ttsctl find 1 "飞船"

# 6. 试听，或重做后立即试听
./ttsctl play 1 42
./ttsctl redo 1 42 --play

# 7. 合并为完整音频
./ttsctl merge 1
```

修改文本后：

```bash
./ttsctl update 1
./ttsctl make 1
```

从 EPUB 提取章节：

```bash
./ttsctl extract book.epub
# 默认输出到 plain_text/，保留 EPUB 内部目录结构
```

---

## 批量转换

目录中准备好多个 `.txt` 文件时，可以使用 `batch.sh`：

```bash
./batch.sh /path/to/txt-dir
./batch.sh -c /path/to/config.yaml /path/to/txt-dir
```

脚本会对每个文件依次执行：

```text
add（已存在则 update） → make → merge
```

- 处理目录下所有 `.txt` 文件，不递归子目录
- 单个文件失败时跳过，继续处理下一个
- 按 Ctrl-C 会终止整个批次，而不是继续下一个文件
- 结束时输出成功/失败数量；存在失败文件时退出码为 1
- 默认使用目标目录的 `config.yaml`，没有时回退到项目根目录的 `config.yaml`
- `config.yaml` 中的相对路径相对于目标目录解析
- 可以通过环境变量 `TTSCTL_BIN` 指定要调用的二进制

---

## 命令一览

| 命令 | 作用 |
| --- | --- |
| `add <file>` | 导入文本文件并创建片段 |
| `update <file\|id>` | 文本变化后增量更新片段 |
| `make <file\|id>` | 合成所有待处理片段 |
| `ls <file\|id> [offset] [limit]` | 查看片段状态和文本 |
| `files` | 列出已注册文件和数字 ID |
| `find <file\|id> <text>` | 按文字搜索片段，输出 seq |
| `play <file\|id> <seq>` | 播放指定片段的音频 |
| `redo <file\|id> <seq> [--play]` | 重做指定片段，可选立即播放 |
| `merge <file\|id>` | 合并全部语音片段为 WAV |
| `clean [--dry-run]` | 清理未被数据库引用的音频 |
| `extract <file.epub> [output_dir]` | 从 EPUB 提取章节文本 |

除 `add` 外，所有按已注册文件操作的命令都支持 **文件 ID** 或原文件名。文件 ID 可以通过 `files` 查看，`add` 成功后也会输出新 ID。

---

## 数据目录

默认情况下：

```text
data/
├── tts.db          # 文件与片段状态
├── audio/          # 按音色和文本哈希保存的 WAV
└── output/         # merge 生成的完整音频
```

`audio/` 下的文件不需要手动管理；`clean` 可以按数据库引用关系清理残留文件。

---

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功 |
| `1` | 参数、配置、IO、数据库或播放器错误 |
| `2` | 合成失败、重做失败、合并前置条件不满足，或 `clean --dry-run` 有待删除项 |
| `130` | 用户中断 |

---

## 适用场景与限制

适合：

- 把长篇小说、文章、剧本转成有声内容
- 需要反复修改文本、分段重做和试听的个人工作流
- 本地或局域网 TTS 服务

限制：

- 不内置 TTS 引擎，需要自备兼容的 HTTP 服务
- 面向单用户 CLI，没有图形界面或在线服务
- 播放依赖系统播放器，不支持内置的暂停、拖动等控制
- 合并只支持 PCM WAV；不同片段格式不一致时会失败
- `add` 和 `update` 默认只处理当前目录下的纯文件名
