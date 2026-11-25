# 云账号最小权限设计（F-0335）

目标：云账号凭据在 Ferry 中以「最小可用权限」接入——面板能列出资源、执行受支持的电源操作、导入账单，但不要求账号持有超出这些动作的授权面。本文给出各主流云厂商的 IAM/权限配置基线，供管理员在厂商控制台创建 Ferry 专用凭据时对照；代码侧的凭据保护措施一并列出。

对应 todo：F-0325/F-0326（加密与内部读取）、F-0330（轮换与宽限）、F-0334（资源范围）、F-0341+（资源同步与操作）。

## 1. Ferry 需要云账号做什么

Ferry 对云账号的动作集（`server/internal/cloudprov` Adapter 契约）：

| 动作 | 契约方法 | 说明 |
| --- | --- | --- |
| 列出/查看资源 | `ListResources` / `DescribeResource` / `RefreshStatus` | 只读 |
| 电源控制 | `PowerOn` / `PowerOff` / `Reboot` | 写，实例级 |
| 强制停止（高危） | `ForceStop` | 写，实例级；面板侧需提权+二次确认（`HighRiskForceStop`） |
| 重装/重建 | `Rebuild` | 写，实例级，**数据丢失**；仅对声明支持的厂商开放 |
| 账单导入 | `ImportBilling` | 只读，账单/计费数据 |

资源创建/销毁目前走 OpenTofu 供给通道（provision），不经 Adapter；Adapter 注册厂商标配后同样适用本文基线（再加实例创建权限即可）。

## 2. 厂商权限基线

原则：**只授上述动作；拒绝删除、网络改配、快照、镜像等超出表一的权限**。多数厂商支持「按标签限定资源」，建议给 Ferry 管理的实例打统一标签（如 `ferry:managed=true`）并把权限圈定在该标签上——这就是 F-0334「按账号授权资源范围」在厂商侧的落地方式。

- **Vultr**：API 个人令牌为全账号权限，无细粒度 IAM——建议用独立子账号/独立令牌，仅给 Ferry 管理的实例打标签，凭据泄露面靠令牌独立+轮换（F-0330）控制。
- **Hetzner Cloud**：Project 级令牌 + Project 内权限边界；建议单开 Project 只放 Ferry 实例，token 权限选 read/write（不含 project 管理删除）。
- **DigitalOcean**：自定义 scope 的 personal access token（`read` + `write`，不勾选 account/billing 管理类 scope）；或 team 级 app token。
- **AWS**：IAM 用户/角色 + 内联策略：`ec2:DescribeInstances`、`ec2:StartInstances`、`ec2:StopInstances`、`ec2:RebootInstances`、`ce:GetCostAndUsage`（账单），资源级 condition 限定 `ec2:ResourceTag/ferry:managed`；**不含** `TerminateInstances`、`RunInstances`（除非走 Adapter 供给）。
- **阿里云**：RAM 用户 + 自定义授权策略：`ecs:DescribeInstances`、`ecs:StartInstance`、`ecs:StopInstance`、`ecs:RebootInstance`、`bss:DescribeInstanceBill`；Resource 限定实例 ID/标签条件；**不含** `DeleteInstance`。
- **腾讯云**：CAM 子账号 + 自定义策略，动作集同阿里云思路（`cvm:DescribeInstances/StartInstance/StopInstance/RebootInstance` + 计费只读）。

以上基线随厂商 IAM 演进而调整；新 Adapter 注册进 `cloudprov` 时，按其 `Capabilities` 声明的动作集对照增删。

## 3. Ferry 侧的凭据保护（已落地）

- **存储**：AccessKey 经统一 secret 入口 AES-256-GCM 加密落库（F-0325），主密钥由 `FERRY_SECRET_KEY` 提供；API 层只回 `has_access_key`，任何接口不回显明文/密文（F-0327）。
- **读取面**：明文仅在两个进程内场景解出——OpenTofu 供给执行（`provision.DecryptKey`）与 Adapter 连接测试/探活（F-0328）；解出后的明文不落日志、不进审计快照、不出进程。
- **轮换**：`POST /api/providers/:id/rotate` 双键轮换，旧密钥保留在 24h 宽限窗内（`PrevAccessKey`），`revoke-previous` 显式提前吊销；全部凭据变更走审计中间件（F-0333）。
- **禁用**：账号禁用后供给与连接测试直接拒绝（F-0331）。

## 4. 资源范围（F-0334 现状与计划）

账号当前可记录作用域（`Provider.Region`，F-0324），连接测试把作用域传给 Adapter（`cloudprov.Account.Region`）。待 F-0341 CloudResource 模型落地后，资源操作接口将在服务端校验「资源 ∈ 账号作用域」，与本文第 2 节的厂商侧标签限定共同构成双层范围控制。
