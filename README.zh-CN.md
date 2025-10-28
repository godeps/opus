![Test](https://github.com/godeps/opus/actions/workflows/test.yml/badge.svg)

# Go Opus（WASM 版本）

本项目是 `gopkg.in/hraban/opus.v2` 的派生版本，将底层 libopus 编译成 WebAssembly 并通过 wazero 在 Go 中运行，彻底移除 CGo 依赖，开箱即可跨平台使用。

- ✅ 纯 Go 依赖链，包含编译好的 WASM 版本 libopus  
- ✅ 支持 PCM ⇄ Opus 的编码和解码  
- ✅ 支持读取 `.opus` / `.ogg`（Opus）流  
- ✅ 便于在 Linux / macOS / Windows / 容器环境中部署  
- ✅ 内置线程安全的 WebAssembly 实例池，可在多 goroutine 间复用  
- ⚠️ 仅生成原始 Opus 数据，不直接写出 `.opus` / `.ogg` 文件  

项目托管在 [github.com/godeps/opus](https://github.com/godeps/opus)。

## 快速开始

### 安装

```bash
go get github.com/godeps/opus
```

或在 `go.mod` 里直接引用：

```go
import "github.com/godeps/opus"
```

### 编码示例

```go
const sampleRate = 48000
const channels = 1

enc, err := opus.NewEncoder(sampleRate, channels, opus.AppVoIP)
if err != nil {
    log.Fatalf("create encoder: %v", err)
}

pcm := make([]int16, sampleRate/50) // 20ms 帧
buf := make([]byte, 1024)
n, err := enc.Encode(pcm, buf)
if err != nil {
    log.Fatalf("encode: %v", err)
}
encoded := buf[:n]
```

### 解码示例

```go
dec, err := opus.NewDecoder(sampleRate, channels)
if err != nil {
    log.Fatalf("create decoder: %v", err)
}

pcmBuf := make([]int16, sampleRate/50)
n, err := dec.Decode(encoded, pcmBuf)
if err != nil {
    log.Fatalf("decode: %v", err)
}
pcmBuf = pcmBuf[:n*channels]
```

更多示例可参考仓库中的 `_test.go` 文件或文档。

## 并发与池化

库内部维护了一个 WebAssembly 模块池。每个编码器/解码器在创建时都会从池中获取独立实例，使用完毕后自动归还，因此无需额外锁即可在多 goroutine 中并行执行。

## 常见问题

- **如何处理丢包？** 使用 `Decoder.DecodePLC` / `DecodeFEC` 等接口完成丢包隐藏或前向纠错。  
- **如何播放 `.ogg` / `.opus` 文件？** 请使用 `Stream` 接口，将文件或网络流包装成 `io.Reader` 传入。  
- **需要容器封装吗？** 本库生成的是原始 Opus 帧，如需写成 `.opus` 文件，需要额外的容器格式（例如 OGG）。  

## 文档

- Go API 文档：<https://pkg.go.dev/github.com/godeps/opus>  
- libopus C API：<https://www.opus-codec.org/docs/opus_api-1.1.3/>

如有问题或改进建议，欢迎提交 Issue 或 PR。祝使用愉快！
