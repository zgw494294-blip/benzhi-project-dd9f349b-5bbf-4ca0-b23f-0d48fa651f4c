# coldchain-route-ledger

`coldchain-route-ledger` 是一个单机运行的社区冷链药品配送账本服务。调度员可以创建带温控范围的配送批次，配送员完成药箱封签、运输交接和温度登记，社区卫生站逐箱验收并记录异常，全部完成后关闭批次并生成可核验的签收凭据。账本保存在本地 JSON 文件中，便于追溯批次状态、审计事件和签收结果。

## 构建

```text
go build ./cmd/coldchain
```

## 运行

```text
go run ./cmd/coldchain --addr :8080 --ledger ./data/coldchain-ledger.json
```

服务提供 JSON HTTP API：

- `POST /v1/batches` 创建配送批次。
- `GET /v1/batches?status=&routeDate=&origin=&destination=&limit=&offset=` 按条件分页查询配送批次摘要。
- `POST /v1/batches/{id}/dispatch` 完成药箱封签并发运。
- `POST /v1/batches/{id}/handoffs` 登记运输交接和温度采样。
- `POST /v1/batches/{id}/receive` 登记单个药箱验收结果。
- `POST /v1/batches/{id}/receive-batch` 一次提交多个药箱验收结果。
- `POST /v1/batches/{id}/close` 关闭批次并生成签收凭据。
- `GET /v1/batches/{id}` 查询批次状态。
- `GET /v1/batches/{id}/events?cursor=` 分页查询审计事件。
- `GET /v1/batches/{id}/receipt` 查询并校验签收凭据。

所有写入接口都使用 `expectedVersion` 进行版本控制。交接接口使用 `idempotencyKey` 支持重复请求安全重放。温度单位使用 `C`，运输温度必须同时满足批次内全部药箱的温控范围。

## 测试

```text
go test ./...
go run ./cmd/coldchain --selfcheck
```

`--selfcheck` 使用内存账本执行一条从创建、发运、交接、逐箱验收到关闭和凭据校验的完整流程，完成后自动退出。
