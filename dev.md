# Kokomi Narrator 开发说明

本文记录实现细节、数据流和设计取舍。用户使用说明见 [README.md](README.md)。

## 技术栈

- Go（版本见 [go.mod](go.mod)）
- CLI：`github.com/spf13/cobra`
- 配置：`gopkg.in/yaml.v3`
- SQLite：`modernc.org/sqlite`（纯 Go，无 CGO）
- 进度条：`github.com/schollz/progressbar/v3`
- 日志：标准库 `log/slog`
- WAV：标准库实现 RIFF/WAVE 解析与写入
- 音频处理：外部 `ffmpeg` / `ffprobe`
- 视频：外部 `audio2video`

## 代码结构

```text
main.go                  程序入口
internal/cli             Cobra 命令、输出、退出码、工作流编排
internal/config          配置加载、默认值、校验、voice_key
internal/store           SQLite schema、文件/片段/BGM 缓存
internal/textparse       UTF-8 文本解析、分类、哈希
internal/wavutil         PCM WAV 解析、校验、编码、静音生成
internal/tts             TTS HTTP 客户端、重试
internal/bgm             曲库扫描、选曲、渲染、混音、曲目清单
internal/silencedetect   ffmpeg silencedetect 封装
internal/pathutil        ~ 路径展开
testdata                 测试音频样本
```

## 配置与派生值

配置项及当前取值示例见 [config.yaml](config.yaml)；字段定义、默认值和校验在 [internal/config/config.go](internal/config/config.go)。

- 配置中的相对路径相对于进程工作目录解析。
- `voice_key = SHA-256(tts.character)` 的前 16 位十六进制。
- 配置校验覆盖 TTS URL、超时、并发数、目录、音量、静音阈值等。

## 存储模型

Schema 定义在 [internal/store/store.go](internal/store/store.go)，本文不重复 SQL。

- `files`：已导入的文本文件。
- `segments`：文件的行级片段和合成状态。
- `bgm_tracks`：BGM 曲库的时长缓存。
- `schema_version`：schema 版本号；迁移逻辑同样在 store.go。
- 删除文件会级联删除片段。
- 连接启用 `foreign_keys` 和 `busy_timeout`，数据库连接数固定为 1，写操作天然串行。

音频路径不写入数据库，而是由 `voice_key + text_hash` 推导：

```text
audio_dir/<voice_key>/<text_hash前2位>/<text_hash>.wav
```

同一音色、同一文本在不同文件间共享同一份音频。写入使用同目录的 `.wav.tmp`，校验通过后原子 `rename`。

## 文本解析

实现见 [internal/textparse/textparse.go](internal/textparse/textparse.go)。

1. 校验 UTF-8，去掉 UTF-8 BOM。
2. `\r\n` 和 `\r` 统一为 `\n`。
3. 按行切分，每行 `TrimSpace`，空行忽略。
4. 含 Unicode 字母或数字的行标记为 `speech`；非空但不含字母/数字的行标记为 `punctuation`。
5. `text_hash = SHA-256(text)`；`seq` 从 1 开始。
6. 标点片段状态初始为 `skipped`。

## 片段状态

状态集合：`pending`、`processing`、`success`、`failed`、`skipped`。

- `pending`：等待合成。
- `processing`：合成请求进行中。
- `success`：已生成过合法 PCM WAV；不代表音频质量。
- `failed`：最近一次合成失败，`error` 保存错误摘要。
- `skipped`：标点片段，不参与合成和合并。

状态由 `add`、`update`、`make`、`redo` 和 `del` 维护，具体流转见后文对应章节。

## 导入与增量更新

### add

实现见 [internal/cli/add.go](internal/cli/add.go)。

- 读取文本并计算原始字节的 `content_hash`。
- 解析片段并计算当前 `voice_key`。
- 在事务中写入 `files` 和 `segments`。
- 检查每个 speech 片段的音频路径；已存在合法 PCM WAV 时直接标记 `success`。

### update

实现见 [internal/cli/update.go](internal/cli/update.go) 和 [internal/store/store.go](internal/store/store.go)。

- 内容哈希未变化时直接返回，`--force` 可强制重新解析。
- 事务内先把旧片段的 seq 改为负数临时值，避免新 seq 与旧 seq 冲突。
- 新片段按以下顺序匹配旧片段：
  1. 相同 `text_hash` 且 `seq` 相同；
  2. 相同 `text_hash` 的任意未匹配片段。
- 匹配成功保留 status/error/attempts，并更新 seq/text/kind。
- 未匹配的旧片段删除；未匹配的新 speech 片段置 pending，新标点片段置 skipped。
- `update` 不检查音频文件是否存在，状态同步留给下一次 `make`。

## TTS 客户端

实现见 [internal/tts/client.go](internal/tts/client.go)。

请求：

- HTTP `POST` 到 `tts.url`，`Content-Type: application/json`。
- 请求体为扁平 JSON：
  - `character`：`tts.character`
  - `text`：片段文本，按 URI 规则编码，空格使用 `%20`
  - `text_language`：固定 `all_zh`
- `tts.headers` 中的键值附加到请求头。

响应与重试：

- 要求 HTTP 200，响应体为合法 PCM RIFF/WAVE，包含可解析的 `fmt ` 和 `data`。
- 单次超时、重试次数和退避由配置控制；总尝试次数为 `retries + 1`。
- 408、429、5xx、网络错误和非法 WAV 响应可重试；其他 4xx 不重试。
- 响应体限制为 64 MiB。
- 失败信息截断后写入片段 `error`，并输出到日志。

## 合成流程

实现见 [internal/cli/make.go](internal/cli/make.go)。

### 启动同步

1. 若 `files.voice_key` 与当前配置不一致，切换 voice_key，并将该文件所有 speech 片段重置为 pending、清空 error、attempts 归零。
2. 以实际音频文件为准修正状态：
   - 合法 PCM WAV → success
   - 不存在 → pending
   - 无法解析、非 PCM、数据不完整 → pending
   - punctuation → skipped
3. 同一 text_hash 只检查一次。
4. 启动同步会按实际音频覆盖残留的 processing：合法音频 → success，否则 → pending。

### 并发合成

- 待处理集合为 speech 且 pending 的片段；按 text_hash 去重后生成任务。
- 使用大小为 `tts.concurrency` 的 worker pool，进度总数按待处理片段数统计。
- 每个任务的流程：
  1. 同哈希片段置 processing，attempts 加 1；
  2. 调用 TTS；
  3. 成功后原子写入 WAV，并将当前文件同哈希 speech 片段置 success；
  4. 失败则置 failed 并记录错误；
  5. 继续处理其他哈希。
- 存在 failed 片段时退出码 2。

### 中断

- 使用 `signal.NotifyContext` 监听 SIGINT/SIGTERM。
- 请求携带 context；中断时将该文件所有 processing 片段恢复 pending，删除本轮任务的 `.tmp`，退出码 130。
- 进程被强杀留下的 processing 由下一次 `make` 启动同步恢复。

### redo

实现见 [internal/cli/redo.go](internal/cli/redo.go)。

- 只接受 speech 片段，且要求当前 voice_key 与配置一致。
- 重新合成新 WAV，校验成功后原子替换旧文件。
- 更新当前文件同哈希 speech 片段为 success。
- 合成失败但旧 WAV 仍合法时保留 success 并播放旧音频；旧 WAV 非法时置 failed。
- 播放器按 `afplay`、`ffplay`、`mpv` 的顺序探测，见 [internal/cli/player.go](internal/cli/player.go)。
- 播放失败只打印 warning，不改变退出码。

## 音频后处理

### merge

实现见 [internal/cli/merge.go](internal/cli/merge.go) 和 [internal/wavutil/wav.go](internal/wavutil/wav.go)。

- 按 seq 读取 speech 片段和对应 WAV；标点片段忽略。
- 只支持 PCM；所有片段的采样率、声道数、位深、blockAlign、byteRate 必须一致。
- 直接拼接各 WAV 的 `data`，用第一个片段的 `fmt` 重写 RIFF/WAVE 头。
- 片段之间插入 `merge.gap_between_segments`；连续的 skipped 片段只产生一个间隔。
- `merge.leading_trailing_gap` 控制首尾是否插入静音。
- 静音按位深填充：8-bit 用 `0x80`，16/24/32-bit 用 `0x00`。
- `merge.overwrite=false` 且输出存在时报错。
- 先写 `.tmp`，校验后原子 `rename`。

### bgm

实现见 [internal/bgm](internal/bgm) 和 [internal/cli/bgm.go](internal/cli/bgm.go)。

目标时长来自 `_voice.wav` 的 RIFF 头，只读取头部，不加载音频数据。

曲库扫描：

- 只扫描 `bgm.dir` 顶层；支持的扩展名定义在 [internal/bgm/scan.go](internal/bgm/scan.go)。
- 使用绝对路径作为缓存 key；按 `size + mtime_ns` 判断是否需要重新 `ffprobe`。
- 新增或变化的曲目使用 4 个 worker 并发探测；消失的曲目按 `dir` 清理。
- 探测失败本次跳过并打印 warning，不写缓存。

选曲：

- 只选择完整曲目；时长大于目标时长的曲目排除。
- 放得下的曲目依次选中，一轮结束后重新打乱继续，直到剩余时长不足以容纳最短曲目。
- 剩余时长随机分配到首部、曲目之间和尾部，最多 3 个位置。
- 只有大于 10ms 的 gap 才生成静音。

渲染：

- 固定 48kHz、双声道、`pcm_s16le`。
- 每个曲目先用 ffmpeg 转码为统一格式的临时 WAV。
- 静音用 `anullsrc` 生成，通过 concat demuxer 拼接。
- BGM 先写 `.tmp`，用 `wavutil` 校验后原子 `rename`。
- 同时生成文本曲目清单，记录实际选中的文件名和开始时间。开始时间按“前置 gap（仅 >10ms）+ 前面曲目的时长”累计；曲目清单同样使用 `.tmp` + `rename`。实现见 [internal/bgm/playlist.go](internal/bgm/playlist.go)，文件格式见 README。

### mix

实现见 [internal/bgm/mix.go](internal/bgm/mix.go)。

- 输入为 `_voice.wav` 和 `_bgm.wav`，两者必须存在且为合法 PCM WAV。
- BGM 先按 `mix.bgm_volume` 调整音量，并重采样/转换到人声的采样率和声道布局。
- 使用 `amix=duration=longest:normalize=0`，避免截断和默认衰减；用 `alimiter` 防止削波。
- 输出格式跟随人声：采样率、声道数、位深不变；只支持单声道或双声道。
- 输出先写 `.tmp`，校验后原子 `rename`。

### video

实现见 [internal/cli/video.go](internal/cli/video.go)。

- 输入为混音生成的最终 `<base>.wav` 和封面图。
- 封面图优先使用 `--image`，否则使用 `video.image`；支持 `~` 展开。
- 调用 `audio2video -a <audio> -i <image> -o <tmp.mp4>`。
- 校验临时文件存在且非空后原子 `rename` 为最终 MP4。
- 中断时删除临时文件，退出码 130。

### EPUB 提取

实现见 [internal/cli/extract.go](internal/cli/extract.go)。

- 使用 `archive/zip` 读取 EPUB，只处理 `.html`、`.xhtml`、`.htm`。
- 拒绝不安全或逃逸输出目录的压缩包路径。
- 将 HTML 转为纯文本，按原目录结构写出 `.txt`。
- 输出文件名优先取正文第一行；不可用时回退到原文件名，并处理重名。
- 单个章节失败只打印 warning；全部失败时返回错误。

## 质量检查

实现见 [internal/cli/check.go](internal/cli/check.go)、[internal/cli/check_cache.go](internal/cli/check_cache.go) 和 [internal/silencedetect/detect.go](internal/silencedetect/detect.go)。

检测：

- 扫描 `audio_dir` 下的片段 WAV，通过 `store.SpeechRefs()` 将 `(voice_key, text_hash)` 映射到所有引用的 `(file_id, seq)`。
- 无 speech 片段引用的 WAV 只打印 warning 并跳过。
- 对每个 WAV 运行 ffmpeg `silencedetect`，检测整个文件，不只看开头。
- 解析 stderr 中的 `silence_start`、`silence_end`、`silence_duration`；无法解码的文件也作为异常。
- 检测并发度来自 `check.concurrency`；结果按 file_id、seq 排序。

缓存：

- 检测完成后，`check` 会用本次结果覆盖当前目录的 `check_results.txt`；无可检测文件时写入空表头。
- 缓存格式和用户编辑方式见 README；解析和校验在 [internal/cli/check_cache.go](internal/cli/check_cache.go)。
- 读取时忽略空行和 `#` 注释；支持制表符和空格分隔；无效行计数并跳过。
- 缓存条目会与当前数据库的 file_id、seq、text_hash 重新校验，过期条目跳过。
- 写入使用 `.tmp` + 原子 `rename`。

`--redo`：

- 缓存存在时直接读取并跳过全量检测；不存在时先执行一次全量检测。
- 每个问题 WAV 重新合成、原子替换、立即复检；失败条目跳过并继续。
- 使用 `tts.concurrency` 并发；重做后把未解决条目写回缓存。
- 退出码和输出约定见 README。

## 删除与清理

### del

实现见 [internal/cli/del.go](internal/cli/del.go) 和 [internal/store/store.go](internal/store/store.go)。

- 事务内统计片段数并删除 `files` 记录。
- `segments` 通过外键级联删除。
- 磁盘音频和 `output` 产物不删除，音频留给 `clean` 按引用关系处理。

### clean

实现见 [internal/cli/clean.go](internal/cli/clean.go)。

- 引用集合来自所有 speech 片段的 `(voice_key, text_hash)`。
- 遍历 `audio_dir`：未引用的 `.wav` 删除，`.tmp` 删除，其他文件打印 warning。
- 路径结构不符合约定的 WAV 不删除。
- `--dry-run` 只列出待删除项；有待删除项时退出码 2。
- 删除后清理空目录；不修改数据库。

## 日志与输出

- 日志使用 `slog`，输出 stderr，级别由 `log_level` 控制。
- 进度条输出 stderr，避免污染 `ls`、`files` 等 stdout 输出。
- 非 TTY 环境退化为逐片段日志。
- 需要返回 2 或 130 的错误通过 [internal/cli/root.go](internal/cli/root.go) 中的 `exitError` 包装；其余错误由 `Execute` 统一映射为 1。用户侧的退出码约定见 README。

## 测试

```bash
go test ./...
go vet ./...
go test -race ./internal/cli ./internal/bgm ./internal/store
```

- 配置、文本解析、SQLite、WAV、BGM 选曲、静音检测和 CLI 工作流都有单元/集成测试。
- 依赖 ffmpeg、ffprobe 或 audio2video 的测试在工具缺失时跳过。
- `testdata/good.wav` 和 `testdata/bad.wav` 用于静音检测和 CLI 集成测试。
