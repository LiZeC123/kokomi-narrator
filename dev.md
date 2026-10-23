# 1. 技术选型

- 语言：Go
- CLI：`github.com/spf13/cobra`
- 配置：`gopkg.in/yaml.v3`
- SQLite：`modernc.org/sqlite`（纯 Go，无 CGO）
- 进度条：`github.com/schollz/progressbar/v3`
- 日志：标准库 `log/slog`
- WAV 处理：标准库自行解析 RIFF/WAVE
- BGM/混音：外部 `ffmpeg` / `ffprobe`（音频库解码、拼接、混音）
- 音频质量检查：外部 `ffmpeg` 的 `silencedetect` 滤镜
- 视频合成：外部 `audio2video`（音频 + 封面图 → MP4）


---

# 2. 配置文件

 默认读取当前目录 `config.yaml`，支持 `--config` 指定。

```yaml
database: ./data/tts.db
audio_dir: ./data/audio
output_dir: ./data/output
log_level: info

tts:
  url: "http://127.0.0.1:8080/tts"
  character: "default"
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

check:
  silence_db: -45
  silence_duration: 1s
  concurrency: 4
```

说明：

- `tts.character` 对应当前音色。
- `voice_key = SHA-256(character)` 取前 16 位十六进制，作为音频一级子目录。
- 音频路径：

```text
audio_dir/<voice_key>/<text_hash前2位>/<text_hash>.wav
```

例如：

```text
data/audio/9f8e7d6c5b4a3210/a1/a1b2c3...（64位）.wav
```

- 临时文件与目标文件同目录，命名为 `<text_hash>.wav.tmp`。
- 修改 `character` 后，`make` 会检测 `files.voice_key` 不一致，自动切换音色并重置该文件的语音片段状态。
- `bgm.dir` 是 BGM 曲库目录，支持 `~`、`~/...`、绝对路径和项目外目录；`bgm` 只读该配置，不提供 `--dir`。
- `mix.bgm_volume` 是混音时 BGM 的线性音量，范围 `0.0 ~ 1.0`，默认 `0.10`。
- `video.image` 是视频封面图，支持 `~`、`~/...` 和绝对路径；可通过 `video --image` 覆盖。
- `check.silence_db` 是 `silencedetect` 的静音阈值（dB），默认 `-45`，只能为 0 或负数。
- `check.silence_duration` 是最短静音时长，默认 `1s`。
- `check.concurrency` 是同时运行的检测进程数，默认 `4`，必须大于 0。

---

# 3. TTS 接口

HTTP 方法：`POST`  
Header：`Content-Type: application/json`

请求体固定为：

```json
{
  "method": "POST",
  "body": {
    "character": "default",
    "text": "要合成的文本"
  }
}
```

说明：

- `method` 是请求体中的业务字段，固定为字符串 `"POST"`，不是 HTTP 方法。
- `character` 来自配置 `tts.character`。
- `text` 为片段文本。
- 配置 `tts.headers` 中的键值会附加到 HTTP Header。
- 成功响应要求 HTTP 200，响应体为合法 RIFF/WAVE 且包含可解析的 `fmt ` 和 `data`，默认只接受 PCM。
- 失败记录 HTTP 状态码、响应体截断内容或网络错误。

重试策略：

- 默认超时 60s，失败重试 2 次，退避 2s。
- 4xx 中 408、429 重试，其余 4xx 不重试。
- 5xx、网络错误、非 WAV 响应可重试。

---

# 4. 数据库设计

`files` + `segments` 保存文本与合成状态；`bgm_tracks` 保存 BGM 曲库的时长缓存。不建 `audio_cache`，不存合成音频路径。

```sql
CREATE TABLE schema_version (
    version INTEGER NOT NULL
);

CREATE TABLE files (
    id            INTEGER PRIMARY KEY,
    filename      TEXT NOT NULL UNIQUE,
    voice_key     TEXT NOT NULL,
    content_hash  TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL
);

CREATE TABLE segments (
    id          INTEGER PRIMARY KEY,
    file_id     INTEGER NOT NULL REFERENCES files(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    text        TEXT NOT NULL,
    text_hash   TEXT NOT NULL,
    kind        TEXT NOT NULL CHECK(kind IN ('speech', 'punctuation')),
    status      TEXT NOT NULL CHECK(status IN (
                    'pending', 'processing', 'success', 'failed', 'skipped'
                )),
    error       TEXT,
    attempts    INTEGER NOT NULL DEFAULT 0,
    updated_at  TEXT NOT NULL,
    UNIQUE(file_id, seq)
);

CREATE INDEX idx_segments_file_status ON segments(file_id, status);
CREATE INDEX idx_segments_hash ON segments(text_hash);

CREATE TABLE bgm_tracks (
    id          INTEGER PRIMARY KEY,
    path        TEXT NOT NULL UNIQUE,
    dir         TEXT NOT NULL,
    filename    TEXT NOT NULL,
    duration_ms INTEGER NOT NULL,
    size        INTEGER NOT NULL,
    mtime_ns    INTEGER NOT NULL,
    updated_at  TEXT NOT NULL
);

CREATE INDEX idx_bgm_tracks_dir ON bgm_tracks(dir);
```

当前 `schema_version = 2`。从版本 1 升级时只新增 `bgm_tracks` 表并更新版本号，不修改原有数据。

`bgm_tracks` 的缓存规则：

- `path` 使用绝对路径，`dir` 是当前曲库的绝对路径。
- 扫描时先比较 `size` 和 `mtime_ns`；一致则直接复用 `duration_ms`，不调用 `ffprobe`。
- 新增或变化的曲目重新探测，成功后 upsert。
- 已消失的曲目按 `dir` 清理。
- 探测失败不写缓存，下次运行继续重试。
- 旧曲库目录留下的缓存不会自动删除，切回该目录时可以继续复用。

连接配置：

```sql
PRAGMA foreign_keys = ON;
PRAGMA busy_timeout = 5000;
```

音频路径完全由 `files.voice_key + segments.text_hash` 推导：

```go
func audioPath(audioDir, voiceKey, textHash string) string {
    return filepath.Join(audioDir, voiceKey, textHash[:2], textHash+".wav")
}
```

---

# 5. 文本解析与分类

流程：

1. 读取文件，校验 UTF-8，非法直接报错。
2. 去掉 UTF-8 BOM。
3. 将 `\r\n`、`\r` 统一为 `\n`。
4. 按 `\n` 切行。
5. 每行 `strings.TrimSpace`。
6. 空行忽略。
7. 分类：
   - 包含 Unicode 字母或数字：`kind = speech`
   - 非空但不含字母和数字：`kind = punctuation`，状态 `skipped`
8. `text_hash = SHA-256(text)`，十六进制 64 字符。
9. `seq` 从 1 开始。

分类函数：

```go
func classify(text string) Kind {
    t := strings.TrimSpace(text)
    if t == "" {
        return Empty
    }
    for _, r := range t {
        if unicode.IsLetter(r) || unicode.IsNumber(r) {
            return Speech
        }
    }
    return Punctuation
}
```

中文汉字属于 `unicode.IsLetter`，中文数字也能识别；中文标点不会被误判为可朗读字符。纯 emoji、纯符号段默认跳过。

---

# 6. CLI 总览

```bash
ttsctl add a.txt
ttsctl update a.txt
ttsctl make a.txt
ttsctl ls a.txt
ttsctl ls a.txt 100 20
ttsctl redo a.txt 12
ttsctl check
ttsctl check --redo
ttsctl merge a.txt
ttsctl bgm a.txt
ttsctl mix a.txt
ttsctl video a.txt
ttsctl clean --dry-run
ttsctl clean
```

通用规则：

- 只接受当前目录下的纯文件名，不接受路径分隔符和 `..`。
- 扩展名必须为 `.txt`，大小写不敏感。
- 文件名作为唯一 key，大小写敏感。
- `add` 已存在报错；`update` 不存在报错。
- `make`、`ls`、`redo`、`merge`、`bgm`、`mix`、`video` 一次只处理一个文件。
- `clean`、`check` 为全局操作。
- `bgm`、`mix`、`video` 支持文件 ID 或原文件名。

退出码：

- `0`：成功
- `1`：参数、配置、IO、数据库错误，或 `ffmpeg`/`ffprobe`/`audio2video` 缺失
- `2`：转换失败、重做失败、合并/BGM/混音/视频前置条件不满足、`clean --dry-run` 有待删除项，或 `check` 发现异常/重做后仍有异常
- `130`：用户中断

---

# 7. `add` 指令

```bash
ttsctl add a.txt
```

流程：

1. 校验文件名，读取文件，计算原始字节的 `content_hash`。
2. 解析片段。
3. 计算当前 `voice_key`。
4. 事务写入：
   - `files`：filename、voice_key、content_hash、时间。
   - `segments`：`speech` 初始 `pending`，`punctuation` 为 `skipped`。
5. 对每个 `speech` 片段，若对应音频路径已存在合法 PCM WAV，直接标记 `success`。
6. 输出添加的片段数和跳过的标点段数。

---

# 8. `update` 增量更新

目标：修改文本后尽量保留未变化片段的转换状态，只调整 `seq`。

流程：

1. 校验文件存在，读取并计算 `content_hash`。
2. 若 `content_hash` 与数据库相同，打印“内容未变化”，直接返回。可加 `--force` 强制重解析。
3. 解析新片段列表。
4. 事务内：
   - 读取旧 `segments`，按 `seq` 排序。
   - 构建 `map[text_hash][]oldSegment`。
   - 先将该文件所有旧记录 `seq` 更新为负数临时值，避免 `UNIQUE(file_id, seq)` 冲突。
   - 遍历新片段，按以下优先级匹配旧记录：
     1. 同 `text_hash` 且 `seq` 相同；
     2. 同 `text_hash` 的任意未匹配记录。
   - 匹配成功：
     - 保留 `status`、`error`、`attempts`；
     - 更新 `seq`、`text`、`kind`；
     - 若新 `kind = punctuation`，状态强制 `skipped`；
     - 若旧 `kind = punctuation` 且新为 `speech`，状态置 `pending`。
   - 未匹配：
     - `speech` 新建为 `pending`；
     - `punctuation` 新建为 `skipped`。
   - 删除仍为负 `seq` 的未匹配旧记录。
   - 更新 `files.content_hash`、`updated_at`。
5. 输出新增、复用、删除片段数量。

`update` 不主动检查音频文件是否存在，状态同步交给 `make` 启动阶段。

---

# 9. `make` 指令

单线程，每次只处理一个文件。

```bash
ttsctl make a.txt
```

## 9.1 启动同步

`make` 启动时先以实际音频文件为准修正数据库状态。

步骤：

1. 加载文件记录，必须存在。
2. 计算当前 `voice_key`。
3. 若 `files.voice_key != current_voice_key`：
   - 更新 `files.voice_key`；
   - 将该文件所有 `speech` 片段置为 `pending`，清空 `error`，`attempts` 清零；
   - `skipped` 不变；
   - 打印音色切换提示。
4. 对该文件执行同步：
   - 对每个 `speech` 片段，检查路径：

```text
audio_dir/<voice_key>/<text_hash前2位>/<text_hash>.wav
```

   - 规则：

| 音频文件情况                         | 数据库新状态 | error  |
| ------------------------------------ | ------------ | ------ |
| 存在且是合法 PCM WAV                 | `success`    | `NULL` |
| 不存在                               | `pending`    | `NULL` |
| 存在但无法解析 / 非 PCM / 数据不完整 | `pending`    | `NULL` |

   - `kind = punctuation` 的片段强制 `status = skipped`，`error = NULL`。
   - 同一 `text_hash` 的多个片段共享检查结果，避免重复 IO。
5. 将残留 `processing` 片段恢复为 `pending`。
6. 输出同步摘要，例如：

```text
sync: 2 missing -> pending, 1 found -> success, 1 processing -> pending
```

同步规则说明：

- 文件存在且合法即视为 `success`，即使数据库原本是 `failed`。
- 文件缺失即视为 `pending`，即使数据库原本是 `success`。
- 非法音频只改状态为 `pending`，不删除文件；下次 `make` 会覆盖写入。
- 音频质量检查由 `check` 指令负责，不影响 `make` 的状态同步。

## 9.2 统计与进度条

- 待处理集合：`kind = speech` 且 `status = pending` 的片段。
- 按 `text_hash` 去重得到本轮唯一哈希列表，但进度条总数按“待处理片段数”统计。
- 进度条输出到 stderr；非 TTY 环境退化为逐片段日志。

## 9.3 逐片段转换

按 `seq` 升序遍历待处理片段，跳过本轮已处理过的 `text_hash`：

1. 将当前文件同 `text_hash` 的所有 `speech` 片段置为 `processing`，`attempts += 1`。
2. 调用 TTS：
   - 使用 `context.WithTimeout`；
   - 请求体固定为第 3 节格式；
   - 失败按配置重试。
3. 成功：
   - 响应体必须是合法 RIFF/WAVE、PCM、包含 `fmt` 和 `data`；
   - 创建父目录 `audio_dir/<voice_key>/<hash前2位>/`；
   - 先写 `<hash>.wav.tmp`，校验后原子 `rename` 为 `<hash>.wav`；
   - 事务更新当前文件同 `text_hash` 的所有 `speech` 片段：`success`，`error = NULL`；
   - 进度条增加受影响片段数。
4. 失败：
   - 事务更新当前文件同 `text_hash` 的所有 `speech` 片段：`failed`，写入错误摘要；
   - 进度条增加受影响片段数；
   - 继续下一个哈希。
5. 用户中断：
   - 当前同哈希片段从 `processing` 恢复为 `pending`；
   - 清理 `.tmp`；
   - 退出码 130。

## 9.4 结束

- 输出成功、失败、跳过、复用数量。
- 存在 `failed` 片段时退出码 2。

---

# 10. `ls` 指令

```bash
ttsctl ls a.txt
ttsctl ls a.txt 100  20
```

参数：

- 第一个参数是偏移值 int，默认 0，表示跳过前 N 个片段。
- 第二个参数是输出条数 int，默认 0，表示不限制，输出全部。
- 负数报错。


输出：

```text
FILE a.txt total=500 voice=9f8e7d6c5b4a3210
showing 100-119 of 500

SEQ  STATUS     KIND        HASH      TEXT
100  success    speech      a1b2c3d4  第100段开头...
101  skipped    punctuation e5f6a7b8  ，。！？
102  pending    speech      c9d0e1f2  第102段开头...
```

规则：

- 文本开头取前 30 个 rune，避免截断中文。
- `failed` 片段追加错误摘要，错误文本截断 50 个 rune。


---

# 11. `redo` 指令

```bash
ttsctl redo a.txt 12
```

流程：

1. 查找该文件 `seq = 12` 的片段。
2. 必须是 `kind = speech`，否则报错。
3. 检查 `files.voice_key == current_voice_key`，不一致报错，提示先执行 `make` 切换音色。
4. 调用 TTS，成功则写 `.tmp`、校验、原子替换目标 wav。
5. 成功：
   - 更新当前文件同 `text_hash` 的所有 `speech` 片段为 `success`；
   - 清空 `error`；
   - 调用系统播放器自动播放该片段。播放失败只打印 warning，不改变退出码。
6. 失败：
   - 若目标 wav 仍存在且合法，保持/设为 `success`，打印错误，播放已有音频，退出码 2；
   - 若目标 wav 不存在或非法，将同哈希片段设为 `failed` 并记录错误，不播放。
7. `redo` 不删除旧音频，只有新音频校验成功后才替换。
8. `redo` 没有 `--play` 参数；播放是默认行为。

---

# 12. `merge` 指令

```bash
ttsctl merge a.txt
```

## 12.1 前置检查

1. 加载文件记录，使用 `files.voice_key` 推导音频路径。
2. 按 `seq` 读取所有片段。
3. 对每个 `speech` 片段：
   - 音频文件必须存在且是合法 PCM WAV；
   - 不强制要求数据库状态为 `success`，以文件为准；
   - 缺失或非法则报错，列出缺失序号，退出码 2。
4. `punctuation` 片段忽略。

## 12.2 WAV 合并

不能简单 `cat` 多个 wav。实现：

1. 解析每个片段 wav 的 `fmt` 和 `data`。
2. 只支持 PCM `audioFormat = 1`。
3. 校验所有片段采样率、声道数、位深、`blockAlign`、`byteRate` 一致。
4. 拼接所有 `data`，并在有效片段之间插入静音。
5. 用第一个片段的 `fmt` chunk 原始数据重写 RIFF/WAVE 头。
6. 输出到 `output_dir/<原文件名去扩展名>_voice.wav`。
7. 先写 `.tmp`，再原子 `rename`。
8. `merge.overwrite = false` 且输出存在时报错。

## 12.3 停顿规则

配置：

```yaml
merge:
  gap_between_segments: 200ms
  leading_trailing_gap: false
```

规则：

- 任意两个“有音频的片段”之间插入一个 `gap_between_segments`。
- 中间连续多个 `skipped` 片段不会叠加停顿，只产生一个间隔。
- `leading_trailing_gap = false` 时，首段前和末段后不加停顿。
- `gap_between_segments = 0` 时不插入静音。

静音生成：

- 帧数 `frames = int(sampleRate * gap.Seconds())`；
- 字节数 `frames * blockAlign`；
- 8-bit unsigned PCM 填充 `0x80`；
- 16/24/32-bit PCM 填充 `0x00`。

最终音频结构：

```text
片段1 PCM + 200ms静音 + 片段2 PCM + 200ms静音 + 片段3 PCM ...
```

---

# 13. `clean` 指令

```bash
ttsctl clean --dry-run
ttsctl clean
```

语义：删除音频目录中没有任何数据库记录引用的文件。

引用集合：

```sql
SELECT DISTINCT f.voice_key, s.text_hash
FROM segments s
JOIN files f ON f.id = s.file_id
WHERE s.kind = 'speech';
```

遍历目录：

```text
audio_dir/<voice_key>/<hash前2位>/*.wav
```

规则：

- `.wav`：`(voice_key, text_hash)` 不在引用集合中，删除。
- `.tmp`：一律删除。
- 非 `.wav` / `.tmp` 文件：打印警告，不删除。
- 删除后可选删除空目录。
- `--dry-run` 只列出待删除项，不删除；有待删除项时退出码 2。
- 默认直接删除并打印统计，不交互确认。
- `clean` 只删文件，不修改数据库。

---

# 14. 中断与断点

- `make` 使用 `signal.NotifyContext` 监听 `SIGINT/SIGTERM`。
- HTTP 请求携带 context。
- 当前片段被取消时：
  - 同哈希 `processing` 片段恢复 `pending`；
  - 删除 `.tmp`；
  - 退出码 130。
- 进程被强杀导致残留 `processing`，下次 `make` 启动同步时统一恢复 `pending`。
- 音频写入使用 `.tmp` + `rename`，不会产生半截 wav。

---

# 15. 日志与进度条

- 日志使用 `slog`，输出 stderr，级别由 `log_level` 控制。
- 进度条输出 stderr，避免污染 `ls` 等 stdout 输出。
- 进度条显示：总数、已完成、失败数、当前哈希前 8 位。
- 非 TTY 环境退化为逐片段日志。

---

# 16. `bgm` 指令

```bash
ttsctl bgm <file|id>
```

目标时长来自 `merge` 生成的人声文件：

```text
output_dir/<base>_voice.wav
```

使用 header-only 方式读取 `fmt` / `data` 头并计算时长，不把整段音频读入内存。文件缺失或非法时退出码 2，并提示先执行 `merge`。

## 16.1 曲库扫描与缓存

- `bgm.dir` 来自配置，不提供命令行参数。
- 支持 `~`、`~/...` 和绝对路径，曲库可以位于项目目录之外。
- 非递归扫描，支持 `.mp3`、`.wav`、`.flac`、`.m4a`、`.aac`、`.ogg`、`.opus`，大小写不敏感。
- 曲目时长写入 `bgm_tracks`，按 `size + mtime_ns` 判断是否需要重新 `ffprobe`。
- 只探测新增或变化的曲目；已消失的曲目从缓存中清理。
- 探测失败的曲目本次跳过并打印 warning，下次运行继续重试。

## 16.2 选曲规则

- 只选择完整曲目。
- 时长大于目标时长的曲目直接排除，不切割、不淡入淡出。
- 如果没有任何可用曲目，退出码 2。
- 曲库不够时循环使用：
  - 每轮打乱曲目顺序；
  - 放得下的曲目依次选中；
  - 放不下的曲目跳过，继续找更短的；
  - 一轮结束仍有剩余时重新打乱再来一轮；
  - 剩余时长比最短曲目还短时停止选曲。
- 剩余时长按 `bgm_tmp.go` 的逻辑随机分配到首部、曲目之间、尾部，最多 3 个位置。
- 只有大于 10ms 的 gap 才生成静音。

## 16.3 渲染

- 固定输出格式：48kHz、双声道、`pcm_s16le`。
- 每个曲目用 ffmpeg 转码为统一格式的临时 WAV。
- 静音用 ffmpeg `anullsrc` 生成。
- 通过 concat demuxer 拼接。
- 输出到 `output_dir/<base>_bgm.wav`：
  - 先写 `<base>_bgm.wav.tmp`；
  - 用 `wavutil` 校验；
  - 原子 `rename`。
- 重复执行会覆盖已有 BGM。
- 中断时清理临时文件和 `.tmp`，退出码 130。

## 16.4 输出摘要

```text
bgm a.txt: library=./data/bgm files=128 cached=124 probed=4 removed=0 invalid=0 target=1h2m3s
bgm a.txt: wrote data/output/a_bgm.wav (42 tracks, 0s silence, 1h2m3.021s)
```

---

# 17. `mix` 指令

```bash
ttsctl mix <file|id>
```

输入：

```text
output_dir/<base>_voice.wav
output_dir/<base>_bgm.wav
```

输出：

```text
output_dir/<base>.wav
```

规则：

- 两个输入必须存在且是合法 PCM WAV，否则退出码 2，并提示先执行 `merge` / `bgm`。
- BGM 音量来自 `mix.bgm_volume`，范围 `0.0 ~ 1.0`，默认 `0.10`。
- 混音以最长输入为时长，避免两者长度接近时截断：
  - `amix=duration=longest`；
  - `normalize=0`，避免人声被默认衰减；
  - `alimiter` 防止削波。
- BGM 重采样到人声的采样率/声道布局。
- 输出格式跟随人声：采样率、声道数、位深不变。
- 只支持单声道或双声道人声。
- 先写 `<base>.wav.tmp`，用 `wavutil` 校验后原子 `rename`。
- 重复执行会覆盖已有最终文件。
- 中断时清理 `.tmp`，退出码 130。


---

# 18. `video` 指令

```bash
ttsctl video <file|id> [--image PATH]
```

输入：

- 最终音频：`output_dir/<base>.wav`（由 `mix` 生成）
- 封面图：优先使用 `--image`，否则使用 `video.image`

输出：

```text
output_dir/<base>.mp4
```

流程：

1. 校验最终音频存在且是合法 PCM WAV：
   - 缺失：退出码 2，提示先执行 `mix`；
   - 非法：退出码 2。
2. 解析封面图路径：
   - 支持 `~`、`~/...` 和绝对路径；
   - 必须是存在的普通文件，否则退出码 2；
   - 支持 `audio2video` 能识别的图片格式（PNG/JPG/JPEG/BMP/GIF）。
3. 从 PATH 查找 `audio2video`，缺失时退出码 1。
4. 调用：

```bash
audio2video -a <最终音频> -i <封面图> -o <output_dir>/<base>.tmp.mp4
```

5. 校验临时文件存在且非空，然后原子 `rename` 为 `output_dir/<base>.mp4`。
6. 重复执行会覆盖已有视频。
7. 中断时删除 `.tmp` 文件，退出码 130。

---

# 19. `check` 指令

```bash
ttsctl check
ttsctl check --redo
```

目标：检查已经生成的片段 WAV 是否存在异常静音，并把问题音频定位到 `file_id + seq`。检测阈值只从配置读取，不提供命令行覆盖参数。检测结果写入当前目录的 `check_results.txt`，`--redo` 优先读取该文件，避免重复执行昂贵的全量检测。

## 19.1 扫描范围

- 扫描 `audio_dir/<voice_key>/<hash前2位>/*.wav`。
- 通过数据库 `segments` + `files` 把 `(voice_key, text_hash)` 映射到所有引用它的 `(file_id, seq)`。
- 一个 WAV 可能被多个文件、多个片段共享；输出时每个 `(file_id, seq)` 各占一行。
- 没有任何 `speech` 片段引用的 WAV 只打印 warning 并跳过，提示执行 `clean`；不参与检测和重做。
- 非 `.wav` 文件忽略；路径结构不符合约定的 WAV 打印 warning 并跳过。
- 不检查 `output_dir` 下的 `_voice.wav`、`_bgm.wav` 和最终 `.wav`，这些文件没有 seq 语义。

## 19.2 检测规则

对每个 WAV 运行：

```bash
ffmpeg -hide_banner -nostdin -nostats \
  -i <wav> \
  -af "silencedetect=n=<check.silence_db>dB:d=<check.silence_duration>" \
  -f null -
```

- 检测整个文件，任意位置出现持续时间达到 `check.silence_duration` 的静音都算异常，不只看开头。
- 解析 ffmpeg stderr 中的 `silence_start`、`silence_end` 和 `silence_duration`。
- ffmpeg 无法解码的文件也作为异常列出，并允许 `--redo`。
- 检测按 `check.concurrency` 并发执行；结果按 `file_id, seq` 排序后输出。
- `--redo` 的合成并发度使用 `tts.concurrency`。
- `ffmpeg` 不在 PATH 时退出码 1。

## 19.3 输出

```text
check: 153 wav file(s), silence_db=-45dB, silence_duration=1s

FILE_ID  SEQ  FILE        HASH      DURATION  SILENCES
1        42   novel.txt   a1b2c3d4  31.66s    3.03s-6.86s (3.84s)
                                              10.00s-12.72s (2.72s)
2        7    other.txt   a1b2c3d4  31.66s    (same wav)
check: 1 problematic wav(s), 2 segment(s) affected
check: saved 2 problem(s) to check_results.txt
```

- 同一 WAV 的静音明细只在第一行展开，后续引用行标记 `(same wav)`。
- 无异常时输出 `check: all clean`，退出码 0。
- 无论是否有异常，`check` 都会覆盖写入当前目录的 `check_results.txt`；无异常时只保留表头。
- 有异常时退出码 2，不做任何数据库状态修改。
- `check` 只做质量检查；片段状态仍表示“是否成功生成过合法 WAV”，不代表音频质量。

## 19.4 `--redo`

启动时：

- 若当前目录存在 `check_results.txt`，直接读取并跳过全量检测。
- 若不存在，则先执行一次 `check` 的全量检测，再进行重做。

对每个有问题的唯一 WAV：

1. 检查该 WAV 对应文件的 `voice_key` 是否等于当前配置音色；不一致时跳过并记录失败原因，提示先执行 `make`。
2. 使用当前 TTS 配置重新合成该 `text_hash` 的文本，网络/HTTP 失败沿用 `tts.retries`。
3. 通过 `.tmp` + 校验 + 原子 `rename` 替换目标 WAV。
4. 更新所有共享该 `text_hash` 的文件的片段状态为 `success`，清空 `error`。
5. 立即用相同阈值重新检测：
   - 无异常：记为 fixed。
   - 仍有异常：记为 still bad，继续处理后续条目。
   - 检测失败：记为 still bad，继续处理后续条目。
6. TTS 或写入失败：
   - 若旧 WAV 仍是合法 PCM，保持/恢复 `success`；
   - 否则将同哈希片段置为 `failed` 并记录错误；
   - 记为 failed，继续处理后续条目。
7. `--redo` 不自动播放音频。
8. 所有条目处理完毕后输出汇总：

```text
redo: fixed=2 wav(s)/4 segment(s), failed=1 wav(s), still_bad=1 wav(s), unresolved_segments=3
failed entries:
  file=3 seq=17 fail.txt hash=a1b2c3d4: HTTP 500: ...
still bad entries:
  file=1 seq=42 novel.txt hash=a1b2c3d4: 3.03s-6.86s (3.84s)
```

- 全部修复时退出码 0。
- 仍有 failed 或 still bad 条目时退出码 2，方便用户按 `file id + seq` 手动复核或再次重做。
- 用户中断时退出码 130；已经完成的 WAV 和数据库更新保留，未处理的条目不再继续。
- 重做结束后会用剩余未解决条目覆盖 `check_results.txt`；全部修复时只保留表头。

## 19.5 检测结果缓存

文件位置：当前工作目录下的 `check_results.txt`，UTF-8 文本。

格式：

```text
# file_id	seq	text_hash	silences
1	42	<64位text_hash>	3.03s-6.86s (3.84s); 10.00s-12.72s (2.72s)
```

规则：

- 每行一个 `(file_id, seq)` 问题条目，字段用制表符分隔；也兼容空格分隔。
- 第一行是 `#` 开头的注释表头，解析时忽略；空行和其他 `#` 行也忽略。
- `silences` 是给人看的静音摘要，`--redo` 不依赖它；未解决条目会写回，其中 still bad 条目使用本次复检的新摘要。
- 用户可以删除某行来跳过该片段，或修改 `file_id` / `seq` / `text_hash` 来指定条目。
- `--redo` 读取缓存后仍会用数据库校验 `file_id + seq` 和 `text_hash`；不匹配的过期条目会打印 warning 并跳过。
- 无法解析的行会打印 warning 并跳过；如果存在无法解析的行且没有任何有效行，退出码 1，提示删除文件或重新执行 `check`。
- 缓存文件存在但只有表头/空行时，视为“没有已知问题”，`--redo` 不会重新检测。
- 删除 `check_results.txt` 后，下一次 `check --redo` 会退化为全量检测。
- 缓存记录的是写入时的检测结果；修改配置阈值后需要重新执行 `check` 才会刷新。
- 缓存写入使用 `check_results.txt.tmp` + 原子 `rename`；写入失败退出码 1。
