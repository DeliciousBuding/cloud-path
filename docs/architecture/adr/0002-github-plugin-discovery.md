# ADR-0002：GitHub Topic + Registry 混合发现

状态：已接受
日期：2026-09-03

## 背景

插件需要一个低门槛、去中心化的发现与分发通道，同时要有可验证的信任。

## 决定

双通道：GitHub Topic `cloudpath-plugin` 负责**发现**，官方 Registry 负责**精选与信任**。
Topic 只是候选集合；安装前强制走验证链（Manifest / 兼容范围 / Release / digest / 可选 attestation）。
分发用 GitHub Release 资产；`plugins.lock` 固定精确版本与摘要。

## 备选方案

- 纯中心商店：门槛高、维护成本高，与「开源生态」定位不符。
- 纯 topic 无 Registry：发现分散，无精选与发布者策略。
- 自建完整 OCI 制品仓库：供应链成熟但首版过重。

## 后果

- 正面：开放、低门槛、可渐进收紧信任。
- 代价：需要维护 Registry 与 CLI 的验证/锁文件逻辑。

详见 [../github-ecosystem.md](../github-ecosystem.md)。