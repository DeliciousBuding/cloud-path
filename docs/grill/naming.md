# Grill: 项目命名与定位
Date: 2026-09-03

## Intent
做一个**通用 IoT 基础设施 monorepo**，不绑定任何具体硬件或业务。STC-B 板是第一个官方
适配的 reference device，取药小药盒只是第一个 reference application（demo）。系统包含
WebUI 管理台、后端、edge 端、上板固件与示例 demo，Go 语言实现。

## Constraints
- 命名不绑定"药盒/取药"等具体业务，避免对外暴露业务细节。
- 现代、简洁、朗朗上口；中文也要好听有意味，但不沾古文典故。
- 意象要大众熟悉、便于产品化（图标 / slogan / 组件名都能自然延伸）。
- 独立 public Git 仓库；任何可识别信息（设备清单、凭据、真实路径）不入库。
- 上板固件沿用 C（Keil），非 Go；其余（edge / backend / webui）Go 实现。

## Key decisions
- Decision: 项目定名 `Cloudpath`（中文「云径」）。Reason: "云 + 径"现代科技意象，中文两字
  朗朗上口、无古文包袱，语义是"设备数据通往云端的路径"，与 edge→backend 数据流一致；
  产品化好做（云/径视觉）。Alternative considered: flotilla / cove / pier / weft / beacon /
  格物 / 知微 / 万象 / 星港 / 星环 —— 均因生僻、古文化或意象不熟悉被否。
- Decision: 药盒降级为 `examples/` 级 reference application，不作为项目名或核心模块。
  Reason: 通用平台与具体业务解耦。Alternative: 以 pillbox-iot 命名，已废弃（GitHub 空仓库待删）。
- Decision: 仓库形态为 monorepo（webui + backend + edge + firmware + examples）。Reason: 单一
  系统、组件强耦合、统一版本与发布。Alternative: 多仓库，割裂，暂不需要。

## Surfaced assumptions
- 原以为项目就是"药盒 IoT"，实际目标是设备无关的通用接入与管理平台。
- 原以为边缘代理单文件 Python 即可，实际需要 Go monorepo 支撑多组件与后续 P2-P4。

## Out of scope
- 具体业务逻辑（服药提醒 / 依从性分析）不作为核心，只在 examples 中体现。