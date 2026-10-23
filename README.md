# Kokomi Narrator

Kokomi Narrator 是一个面向个人使用的长文本 TTS 工作流工具。`ttsctl` 把文本切成片段、调用 TTS 服务、管理合成状态，并把片段加工成有声书音频或视频。

它不内置 TTS 引擎，需要连接外部 HTTP 服务。实现细节见 [dev.md](dev.md)。

## 功能

- 按行导入 `.txt`，纯标点行自动跳过
- 修改文本后只重新处理变化的片段
- 支持并发合成、失败重试、断点续跑
- 按文件查看片段状态、搜索文本、试听和重做
- 检查异常长静音，并批量重做问题片段
- 合并人声，随机生成 BGM，混音为最终 WAV
- 将最终音频和封面图合成为 MP4
- 从 EPUB 提取章节文本
- 删除已导入项目并清理无引用音频
- 使用数字文件 ID 代替长文件名

## 环境要求

- Go 1.25.1 或更高版本
- 一个兼容的 TTS HTTP 服务，返回 PCM WAV；接口约定见 [dev.md](dev.md) 的“TTS 客户端”一节
- `bgm`：`ffmpeg`、`ffprobe`
- `mix`、`check`：`ffmpeg`
- `video`：`audio2video`
- 试听：系统播放器；支持 `afplay`、`ffplay`、`mpv`

## 安装与配置

### 构建

```bash
cd kokomi-narrator
go build -o ttsctl .
```

### 配置

默认读取当前目录的 `config.yaml`，也可以用 `--config` 指定其他路径。配置项及当前取值示例见 [config.yaml](config.yaml)。

配置分为 TTS、合并、BGM、混音、视频和静音检查几组。首次使用至少确认 TTS 地址与音色、音频与输出目录、BGM 曲库和封面图路径。

## 基本流程

```bash
# 1. 导入文本并查看文件 ID
./ttsctl add novel.txt
./ttsctl files

# 2. 合成并查看片段
./ttsctl make 1
./ttsctl ls 1

# 3. 合并人声
./ttsctl merge 1

# 4. 生成 BGM、混音、合成视频
./ttsctl bgm 1
./ttsctl mix 1
./ttsctl video 1
```

输出文件默认位于 `config.yaml` 的 `output_dir`：

| 命令 | 输出 |
| --- | --- |
| `merge` | `<文件名>_voice.wav` |
| `bgm` | `<文件名>_bgm.wav`、`<文件名>_bgm.txt` |
| `mix` | `<文件名>.wav` |
| `video` | `<文件名>.mp4` |

重复执行会覆盖已有输出。

## BGM 曲目清单

`bgm` 会生成 `<文件名>_bgm.txt`，记录实际抽中的曲目及开始时间，便于按时间定位。曲库目录（配置中的 `bgm.dir`）只扫描顶层，支持 mp3、wav、flac、m4a、aac、ogg、opus：

```text
# start	file
00:00:00.000	one.mp3
00:03:21.500	two.flac
```

## 常用操作

### 修改文本

```bash
./ttsctl update 1
./ttsctl make 1
```

`update` 会尽量复用未变化片段的合成状态。内容未变化时默认跳过，可用 `update --force` 强制重新解析。

### 定位、试听和重做

```bash
./ttsctl find 1 "飞船"
./ttsctl play 1 42
./ttsctl redo 1 42
```

`redo` 会重新合成指定片段，成功后自动播放。

### 检查异常音频

TTS 服务偶尔会返回带有异常长静音的 WAV。`check` 会扫描所有片段，并列出问题所在的文件 ID 和 seq：

```bash
./ttsctl check
```

```text
check: 153 wav file(s), silence_db=-45dB, silence_duration=1s

FILE_ID  SEQ  FILE        HASH      DURATION  SILENCES
1        42   novel.txt   a1b2c3d4  31.66s    3.03s-6.86s (3.84s)
                                              10.00s-12.72s (2.72s)
check: 1 problematic wav(s), 1 segment(s) affected
check: saved 1 problem(s) to check_results.txt
```

检测结果会写入当前目录的 `check_results.txt`，每次执行覆盖。文件是普通文本，可以直接编辑或删除某一行：

```text
# file_id	seq	text_hash	silences
1	42	<64位text_hash>	3.03s-6.86s (3.84s); 10.00s-12.72s (2.72s)
```

使用 `--redo` 会自动重做缓存中的问题片段：

```bash
./ttsctl check --redo
```

- 存在 `check_results.txt` 时跳过全量检测，直接按缓存重做；不存在时先执行一次检测。
- 每个问题音频重做后立即复检；失败的条目跳过并继续处理后续条目。
- 结束后输出修复数量，以及仍然失败的 `file id`、`seq`、文件名和原因。
- 未解决的条目会写回缓存；全部修复时缓存只保留表头。
- 无法解析的缓存行会被忽略；如果存在无法解析的行且没有任何有效行，`--redo` 会报错并提示删除或重新生成缓存。
- 全部修复退出码 0；仍有失败条目退出码 2。
- 删除 `check_results.txt` 可强制重新检测。
- 只检测长静音，不检测一般性杂音；检测整段音频，刻意的长停顿也会被标记。阈值在 [config.yaml](config.yaml) 的 `check` 段调整。

### 删除项目

```bash
./ttsctl del 1
./ttsctl clean
```

`del` 只删除数据库中的文件记录和片段，不删除音频文件，也不删除 `output/` 下的合并、BGM、混音或视频产物。之后执行 `clean` 清理不再被任何片段引用的音频。

### 从 EPUB 导入

```bash
./ttsctl extract book.epub
./ttsctl extract book.epub plain_text/novel
./ttsctl extract -o plain_text/novel book.epub
```

默认输出到 `plain_text/`，保留 EPUB 内部目录结构。

## 命令参考

| 命令 | 作用 |
| --- | --- |
| `add <file>` | 导入文本文件并创建片段 |
| `del <file\|id>` | 删除已导入文件及其片段记录（音频留给 `clean` 清理） |
| `update <file\|id> [--force]` | 文本变化后增量更新片段 |
| `make <file\|id>` | 合成所有待处理片段 |
| `ls <file\|id> [offset] [limit]` | 查看片段状态和文本 |
| `files` | 列出已注册文件和数字 ID |
| `find <file\|id> <text>` | 按文字搜索片段，输出 seq |
| `play <file\|id> <seq>` | 播放指定片段的音频 |
| `redo <file\|id> <seq>` | 重做指定片段并自动播放 |
| `check [--redo]` | 检查片段 WAV 的异常长静音并缓存结果，可选从缓存自动重做 |
| `merge <file\|id>` | 合并全部语音片段为 `<文件名>_voice.wav` |
| `bgm <file\|id>` | 生成与人声等长的 BGM，输出 `<文件名>_bgm.wav` 和 `<文件名>_bgm.txt` |
| `mix <file\|id>` | 混合人声与 BGM，输出最终 `<文件名>.wav` |
| `video <file\|id> [--image PATH]` | 用最终音频和封面图生成 `<文件名>.mp4`，封面支持 PNG/JPG/JPEG/BMP/GIF |
| `clean [--dry-run]` | 清理未被数据库引用的音频 |
| `extract <file.epub> [output_dir]` | 从 EPUB 提取章节文本 |

命令中标记为 `<file|id>` 的参数都支持文件 ID 或原文件名。文件 ID 可以通过 `files` 查看，`add` 成功后也会输出新 ID。

## 批量转换

目录中准备好多个 `.txt` 文件时，可以使用 [batch.sh](batch.sh)：

```bash
./batch.sh /path/to/txt-dir
./batch.sh -c /path/to/config.yaml /path/to/txt-dir
./batch.sh --with-bgm -c /path/to/config.yaml /path/to/txt-dir
```

每个文件依次执行 `add`（已存在则 `update`）→ `make` → `merge`。加上 `--with-bgm` 后继续执行 `bgm` → `mix`。

- 只处理目标目录顶层的 `.txt` 文件
- 单个文件失败时跳过，继续处理下一个
- 按 Ctrl-C 会终止整个批次
- 结束后输出成功/失败数量；存在失败文件时退出码为 1
- 默认使用目标目录的 `config.yaml`，没有时回退到项目根目录的 `config.yaml`
- 配置中的相对路径相对于目标目录解析
- 可用环境变量 `TTSCTL_BIN` 指定要调用的二进制

## 退出码

| 退出码 | 含义 |
| --- | --- |
| `0` | 成功 |
| `1` | 参数、配置、IO、数据库、播放器错误，或外部命令缺失 |
| `2` | 合成/重做/前置条件/输出失败，`check` 发现问题，或 `clean --dry-run` 有待删除项 |
| `130` | 用户中断 |

## 限制

- 面向单用户 CLI，不提供图形界面或在线服务
- 播放依赖系统播放器，不支持暂停、拖动等控制
- 合并要求所有片段格式一致，且只支持 PCM WAV
- BGM 只选择完整曲目，固定输出 48kHz 双声道 PCM，长音频文件会比较大
- `add` 和 `update` 只处理当前目录下的纯 `.txt` 文件名
