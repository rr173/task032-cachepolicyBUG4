# task032-cachepolicy

这是一个纯 Go 的缓存淘汰策略引擎，为容量受限的内存缓存提供 LRU、LFU（含 LRU tie-break）和 TTL 过期回收能力，并通过 HTTP+JSON 暴露创建实例、读写键值、统计和删除接口。不依赖数据库或外部服务。

## 标准命令

```bash
GOTOOLCHAIN=local go test -mod=vendor -count=1 ./...
GOTOOLCHAIN=local go vet -mod=vendor ./...
CGO_ENABLED=0 GOTOOLCHAIN=local go build -mod=vendor ./...
GOTOOLCHAIN=local go run -mod=vendor . --smoke-test
GOTOOLCHAIN=local go run -mod=vendor . server --addr :8080
```

`--smoke-test` 使用进程内 HTTP 服务覆盖 LRU、LFU、TTL 和错误响应，并在完成后自行退出；服务模式监听 HTTP。

## Benzhi Docker

`build_benzhi_docker.sh` 使用固定的 `benzhi.Dockerfile` 构建镜像，参数依次为镜像名和平台，默认值为 `my-project` 与 `linux/amd64`。例如：

```bash
bash build_benzhi_docker.sh task032-cachepolicy linux/amd64
docker run --rm -it task032-cachepolicy:latest
```

容器启动后进入 Bash shell；项目构建步骤在镜像构建阶段执行。
