# BENZHI_README

## 项目说明
- 项目：benzhi-project-dd9f349b-5bbf-4ca0-b23f-0d48fa651f4c
- 项目用途：coldchain-route-ledger 是一个单机可运行的社区冷链药品配送账本服务。服务通过 JSON HTTP API 与同源浏览器工作台支持配送批次从创建、药箱封签发运、运输交接温度登记、逐箱验收，到关闭归档和签收凭据核验的完整流程，所有记录保存于本地 JSON 账本。
- Go 工具链：`golang:1.22`
- 前端工具链：无

## 标准构建、运行和测试命令
进入容器后执行：
cd '/app' && GOTOOLCHAIN=local go build ./...
cd /app && GOTOOLCHAIN=local go run ./cmd/coldchain --selfcheck
cd '/app' && GOTOOLCHAIN=local go test ./...

## Docker 构建和进入容器
chmod +x build_benzhi_docker.sh
./build_benzhi_docker.sh benzhi-project-dd9f349b-5bbf-4ca0-b23f-0d48fa651f4c-amd64 linux/amd64
./build_benzhi_docker.sh benzhi-project-dd9f349b-5bbf-4ca0-b23f-0d48fa651f4c-arm64 linux/arm64
docker run -it benzhi-project-dd9f349b-5bbf-4ca0-b23f-0d48fa651f4c-amd64:latest

## 题目验证命令
1. 预期退出码 0：`go test ./...`
2. 预期退出码 0：`go run ./cmd/coldchain --selfcheck`
