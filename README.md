# Kokomi Narrator

把长篇文本变成有声书的个人 TTS 工作流工具。

`ttsctl` 负责把文本切成片段、调用 TTS 服务、管理每段的合成状态，并在需要时试听、重做、合并人声、合成 BGM 和混音。它不内置 TTS 引擎，而是连接一个返回 WAV 音频的 HTTP 服务。

---

## 为什么做这个

长篇文本的语音合成通常不是一次完成的：文本会反复修改，个别片段可能需要重做，服务也可能失败或中断。如果每次全文重新合成，既慢又浪费。

Kokomi Narrator 把“文本文件”拆成“语音片段”来管理：

- 已经合成过的片段不会重复请求
- 修改文本后只处理真正变化的片段
- 失败的片段可以单独重试
- 听感不对的片段可以直接定位、重做并复听
- 最终把全部片段合并成人声，并可选合成 BGM、混音为最终 WAV

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
- **试听与返修**：播放某一段音频；重做后自动播放新音频
- **人声合并**：按顺序合并片段，并在片段之间插入可配置的静音，输出 `<文件名>_voice.wav`
- **BGM 合成**：从配置的曲库随机选曲，按人声时长生成 `<文件名>_bgm.wav`
- **最终混音**：将人声与 BGM 按可配置音量混合为 `<文件名>.wav`，以较长输入为时长
- **视频合成**：将最终音频和封面图合成为 `<文件名>.mp4`
- **曲目缓存**：BGM 曲目时长写入 SQLite，只有新增或变化的文件才重新探测
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
merge → output/<文件名>_voice.wav
   │
   ├── BGM 曲库 → bgm → output/<文件名>_bgm.wav
   │
   ▼
mix → output/<文件名>.wav
   │
   └── video → output/<文件名>.mp4
```

几个关键设计：

- **音频内容寻址**：音频路径由“音色标识 + 文本 SHA-256”推导，不把路径存进数据库，因此相同文本可以跨文件复用。
- **片段状态机**：每个片段有 `pending / processing / success / failed / skipped` 状态。`make` 启动时会先根据实际音频文件修正数据库状态。
- **原子写入**：音频先写临时文件，校验通过后再替换目标文件，避免留下半截 WAV。
- **增量更新**：文本变化时按内容哈希匹配旧片段，尽量保留原状态，只调整片段顺序。
- **安全中断**：中断时把正在处理的片段恢复为 `pending`，下次继续。
- **BGM 曲目缓存**：曲目时长按 `size + mtime` 缓存，未变化的曲目不再调用 `ffprobe`。

---

## 快速开始

### 环境要求

- Go 1.25.1 或更高版本
- 一个可用的 TTS HTTP 服务，返回 PCM WAV
- `bgm` 需要 `ffmpeg` 和 `ffprobe` 在 PATH 中
- `mix` 需要 `ffmpeg` 在 PATH 中
- `video` 需要 `audio2video` 在 PATH 中
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

bgm:
  dir: ./data/bgm        # 支持 ~/... 和绝对路径

mix:
  bgm_volume: 0.10

video:
  image: ./data/image.jpg
```

常用配置说明：

- `tts.character`：音色标识，修改后 `make` 会重新合成
- `tts.concurrency`：同时请求 TTS 的数量，默认 2
- `tts.headers`：需要认证时附加的请求头
- `merge.gap_between_segments`：片段之间的静音长度
- `merge.leading_trailing_gap`：是否在整段音频首尾也插入静音
- `bgm.dir`：BGM 曲库目录，支持 `~/...`、绝对路径和项目外目录，`bgm` 会扫描该目录下的常见音频格式
- `mix.bgm_volume`：混音时 BGM 的音量，范围 `0.0 ~ 1.0`，默认 `0.10` 即 10%
- `video.image`：视频封面图，支持 `~/...` 和绝对路径；可用 `video --image` 覆盖

> 输出命名：`merge` 生成 `<文件名>_voice.wav`，`bgm` 生成 `<文件名>_bgm.wav`，`mix` 生成最终 `<文件名>.wav`，`video` 生成 `<文件名>.mp4`。从旧版本升级后需要重新执行一次 `merge`。

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

# 6. 试听，或重做（重做成功后会自动播放）
./ttsctl play 1 42
./ttsctl redo 1 42

# 7. 合并人声
./ttsctl merge 1
# -> data/output/novel_voice.wav

# 8. 根据人声时长生成 BGM
./ttsctl bgm 1
# -> data/output/novel_bgm.wav

# 9. 混音为最终音频
./ttsctl mix 1
# -> data/output/novel.wav

# 10. 合成为视频
./ttsctl video 1
# -> data/output/novel.mp4
# 封面默认读取 video.image，可用 --image 覆盖
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
./batch.sh --with-bgm -c /path/to/config.yaml /path/to/txt-dir
```

脚本会对每个文件依次执行：

```text
add（已存在则 update） → make → merge
```

加上 `--with-bgm` 后继续执行：

```text
bgm → mix
```

- 处理目录下所有 `.txt` 文件，不递归子目录
- 默认只生成 `<文件名>_voice.wav`；`--with-bgm` 会继续生成 `_bgm.wav` 和最终 `<文件名>.wav`
- 单个文件失败时跳过，继续处理下一个
- 按 Ctrl-C 会终止整个批次，而不是继续下一个文件
- 结束时输出成功/失败数量；存在失败文件时退出码为 1
- 默认使用目标目录的 `config.yaml`，没有时回退到项目根目录的 `config.yaml`
- `config.yaml` 中的相对路径相对于目标目录解析
- 可以通过环境变量 `TTSCTL_BIN` 指定要调用的二进制
- `--with-bgm` 需要配置有效的 `bgm.dir` 和 PATH 中的 `ffmpeg`/`ffprobe`

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
| `redo <file\|id> <seq>` | 重做指定片段并自动播放 |
| `merge <file\|id>` | 合并全部语音片段为 `<文件名>_voice.wav` |
| `bgm <file\|id>` | 生成与人声等长的 BGM，输出 `<文件名>_bgm.wav` |
| `mix <file\|id>` | 混合人声与 BGM，输出最终 `<文件名>.wav` |
| `video <file\|id> [--image PATH]` | 用最终音频和封面图生成 `<文件名>.mp4` |
| `clean [--dry-run]` | 清理未被数据库引用的音频 |
| `extract <file.epub> [output_dir]` | 从 EPUB 提取章节文本 |

除 `add` 外，所有按已注册文件操作的命令都支持 **文件 ID** 或原文件名。文件 ID 可以通过 `files` 查看，`add` 成功后也会输出新 ID。

---

## 数据目录

默认情况下：

```text
data/
├── tts.db          # 文件、片段状态和 BGM 曲目缓存
├── audio/          # 按音色和文本哈希保存的 WAV
├── bgm/            # BGM 曲库
└── output/         # _voice.wav、_bgm.wav、最终 .wav 和 .mp4
```

`audio/` 下的文件不需要手动管理；`clean` 可以按数据库引用关系清理残留文件。`output/` 下的文件由 `merge`、`bgm`、`mix`、`video` 生成，重复执行会覆盖。

---

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功 |
| `1` | 参数、配置、IO、数据库、播放器错误，或 `ffmpeg`/`ffprobe`/`audio2video` 缺失 |
| `2` | 合成失败、重做失败、合并/BGM/混音/视频前置条件不满足，或 `clean --dry-run` 有待删除项 |
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
- `bgm` 需要 `ffmpeg`/`ffprobe`，生成的 BGM 固定为 48kHz 双声道 PCM WAV，长音频文件会比较大
- `bgm` 只选择完整曲目；比目标时长长的曲目会被排除，剩余不足一首的时长用静音补齐
- `mix` 需要 `ffmpeg`，BGM 音量由 `mix.bgm_volume` 控制
- `mix` 以较长输入为时长；正常情况下 BGM 与人声等长，差异较大时会打印 warning
- `video` 需要 `audio2video` 在 PATH 中，并先通过 `mix` 生成最终音频
- 视频封面支持 `audio2video` 能识别的格式（PNG/JPG/JPEG/BMP/GIF）
- `add` 和 `update` 默认只处理当前目录下的纯文件名
