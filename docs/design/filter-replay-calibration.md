# 过滤策略历史回放与校准报告

本文用于 P4 S11 的脱敏历史邮件离线回放。它生成校准证据，不发布策略、不修改节点配置，也不代表业务方已经批准生产 enforce。

## 1. 标注清单

manifest 使用 UTF-8 JSON，EML 路径相对于 manifest 所在目录，也可以使用绝对路径。生产原始 EML 和真实 manifest 不得提交到仓库。

```json
{
  "schema_version": 1,
  "guideline_version": "v0.1",
  "samples": [
    {
      "sample_id": "batch-20260722-0001",
      "eml": "maildir/new/message-1",
      "mailbox": "replay@example.invalid",
      "label": "ad",
      "split": "training",
      "received_at": "2026-06-01T10:00:00Z"
    }
  ]
}
```

约束：

- `sample_id` 在 manifest 内唯一，只使用脱敏批次编号。
- `label` 只能是 `ad / transactional / other / uncertain`；`uncertain` 会计数但不进入矩阵和阈值验收。
- `split` 只能是 `training / calibration / validation`。
- `received_at` 必须是样本入站时间的 RFC3339 快照；禁止用可伪造的邮件 `Date` 头或复制后变化的文件 mtime 代替。
- 三个有效 split 都必须有样本；时间范围必须按 training、calibration、validation 依次完全分离。
- 同一规范化 Header From 域不能跨 split。报告只输出该域 SHA-256 的前 16 个十六进制字符，不输出真实域名。
- 标注依据和复核流程遵守[广告邮件过滤标注准则](spam-filter-labeling-guidelines.md)。

## 2. 运行

使用从已校验 revision 导出的 canonical ad bundle：

```bash
mail-node-filter-replay \
  --manifest /secure/replay/manifest.json \
  --server-id 1 \
  --ad-bundle /secure/replay/ad-bundle.json \
  > /secure/replay/report.json
```

manifest 模式要求 `--server-id` 和 `--ad-bundle`，不能与单封回放的 `--eml` 同时使用。工具只读取文件，不移动、标记、隔离或转发邮件；`filter.auto_quarantine_enabled` 不参与离线报告。

全 shadow bundle 的候选 action/score 从独立 `shadow-graph` 读取。没有 shadow 图时使用 enforce 图结果。这样 `ad-seed-v1` 保持线上零副作用，同时仍能产生 would-tag/would-quarantine 校准数据。

## 3. 报告

报告固定包含：

- ad revision、checksum、tag/quarantine threshold；
- `ad / transactional / other` 各标签对 `allow / tag / quarantine` 的矩阵；
- 三个 split 的样本量和时间范围，以及时间隔离是否通过；
- 发件域哈希分层的标签/action 数量，以及跨 split 域泄漏清单；
- 距 tag 或 quarantine 阈值 1.000 分以内的样本；
- 全部 would-quarantine 样本。

样本明细只包含 `sample_id`、label、split、域哈希、score 和 action。报告不包含 EML 路径、Message-ID、邮箱正文、HTML、附件、真实发件地址或域名。

## 4. 进入 canary 前

报告生成不等于通过生产启用门槛。进入 `dual_filter` canary 前仍需：

1. `time_isolation_passed=true` 且 `domain_isolation_passed=true`。
2. 阈值附近与 would-quarantine 样本完成双人复核。
3. 业务方书面确认误隔离率目标、shadow 最短时长和最小样本量。
4. 所有健康节点的 manual/ad applied revision 和 checksum 一致。
5. 放行幂等、outbox 恢复、策略回滚与 `legacy` 快速回退演练通过。

任何一项未满足时，生产保持 `filter.auto_quarantine_enabled=false`，只允许 shadow 或 tag。

## 5. Shadow 采集切换

历史 baseline 不足时，可以发布全 shadow bundle 并进入 `dual_shadow` 收集真实流量，但真实动作仍由 legacy 引擎决定。使用与控制面相同 revision 构建的 `filter-shadow-ops`，按以下顺序执行：

```bash
filter-shadow-ops --action status --revision 1

filter-shadow-ops \
  --action publish-shadow \
  --revision 1 \
  --expected-ad-checksum <baseline-ad-checksum> \
  --confirm PUBLISH_SHADOW

# 等待所有节点的 manual/ad desired_revision、applied_revision 和 checksum 收敛后：
filter-shadow-ops \
  --action enable-dual-shadow \
  --revision 1 \
  --expected-ad-checksum <baseline-ad-checksum> \
  --confirm ENABLE_DUAL_SHADOW
```

命令只接受空 manual revision 和 detector/composite 全为 `shadow` 的 ad revision；存在节点级 `filter.engine_mode` 或 `filter.auto_quarantine_enabled` override、全局自动隔离不为 false、checksum 不符、任一节点缺少策略状态或 applied 未收敛时均拒绝变更。异常时回退：

```bash
filter-shadow-ops --action legacy --confirm RETURN_LEGACY
```

`dual_shadow` 不代表 canary enforce 已获批准。进入 `dual_filter` 仍必须满足第 4 节全部门槛。
