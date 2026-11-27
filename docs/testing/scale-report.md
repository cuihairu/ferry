# 规模压测报告（F-2076）

数据来源：`deploy/scale-test.sh`（CI：test.yml deploy-scripts 的 `scale load smoke` 步骤），
每次 CI 运行都会重新产出同样的数字。本文件记录 2026-11-27 的验收运行结果
（commit 9c68c3c，本地 Linux 14 核，三连跑 exit=0，数字取最后一跑）。

## 测试环境

| 项 | 值 |
| --- | --- |
| 环境 | 本地 Linux 7.0.0-44-generic，14 CPU（CI 为 ubuntu-latest） |
| 被测 | ferry-server 真二进制，sqlite（WAL，busy_timeout 5000ms），临时数据目录 |
| 协议客户端 | 真 `packages/agentproto` + gorilla/websocket 临时模块（无 mock） |

## 数据规模与并发量

| 项 | 值 |
| --- | --- |
| 注册节点 | 50（8 并发注册，<1s） |
| 长连接 | 50 条并发 WS，全部 hello 握手成功，health 报告 agents_online=50 |
| 心跳 | 3 轮 × 50 = 150 次，全部 ack |
| 探测报告 | 50 agent × 10 batch × 2 items = 1000 行，落库恰好 1000（reachable/healthy 各 1000） |
| 订阅请求 | 400 次，并发 20，1s 内完成 |
| 流量帧 | 50 agent × 4 帧 = 200 帧，逐字节合计 300000B（50 节点 × 6000B，双台账 delta=0） |
| 配置批量下发 | probe_tasks → 50 在线节点，50/50 delivered=true（agent probe_task_ack OK） |

## 延迟（P50/P95/P99，毫秒）

| 操作 | n | P50 | P95 | P99 | max |
| --- | --- | --- | --- | --- | --- |
| 心跳 ack | 150 | 7.1 | 26.2 | 33.4 | 39.2 |
| 探测报告 ack | 500 | 38.5 | 100.9 | 131.6 | 144.4 |
| 流量帧 ack | 200 | 32.8 | 67.6 | 93.8 | 106.9 |

订阅负载行为：200=30、429=370、5xx=0 —— 每令牌 30 次/分钟的 subLimiter
（router.go）在热突发下正确节流，渲染零错误（vless config.uuid 就位的节点）。

## 资源占用

- server RSS：约 50 MB（三连跑 49.3 / 51.1 / 50.4 MB），50 长连接 + 上述负载全程。

## 故障腿（F-2073）

- 25/50 连接同时断开 → agents_online=25（server 同步标记 offline）；
- 客户端退出全量断开 → agents_online=0，/api/health 仍 200。

## 候选 Path 调度规模（F-2066）

`server/internal/alloc/alloc_scale_test.go`：

- 正确性（TestPickScaleDeterministicAndCorrect，5000 候选池）：五种策略
  （least_conn/cost_first/perf_first/balanced/priority）健康门仍过滤病态候选
  （1/5 病态池全部落选）、least_conn 胜者与独立 oracle（最小 conns/capacity +
  名字 tie-break）一致、50 次重复选择结果零漂移、全病态池正确拒绝分配；
- 吞吐（BenchmarkPickLargePool，5000 候选轮换四策略）：约 1.36 ms/op
  （i9-10880H，2026-11-27）——调度按连接粒度发生，单次选择远低于连接建立
  预算。

## 账单大批量导入（F-2070）

`server/internal/billing/import_scale_test.go`（TestImportFileLargeBatch，2026-11-27）：

- 5000 行账单走真实 ImportFile 全路径（JSON 解析→去重键分组→落库）：run
  succeeded、added=5000、落库 CostItem=5000、金额逐分求和一致，首过 1.47s；
- 同文件重导入：5000/5000 全部去重为 skipped，CostRecord 仍 5000 —— 量级下
  无一分双计（F-0529）。

## 结论

控制面在 50 节点规模下心跳/探测/流量/订阅/配置下发全链路无 5xx、
台账逐字节准确、故障面行为正确。更大规模（500+）不属于本 smoke 的
验收门槛；规模族余下条目见 todo.md F-9804。
