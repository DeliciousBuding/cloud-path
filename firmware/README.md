# firmware — 设备侧说明

Cloudpath 是上位机侧平台（edge + server + 管理台），**不含任何厂商固件、SDK 或库文件，
也不规定任何设备线协议**。设备怎么说话由 Driver Plugin 决定；平台只认
[docs/protocol.md](../docs/protocol.md) 里的 Device / Entity / Capability / Observation / Event / Command。

参考实现：[`cloud-path-driver-stcb`](https://github.com/DeliciousBuding/cloud-path-driver-stcb)
对接的 STC-B 学习板（C 固件，Keil 工具链，固件在
[`stcb-firmware-sdk`](https://github.com/DeliciousBuding/stcb-firmware-sdk)），两者都不在本仓库内。

接入自己的设备时：写一个 Driver Plugin 就够了，**不需要动本仓库**；只有当你同时要写设备侧
固件时，下面这些从真板上换来的经验能少踩几个坑。

## 固件侧设计建议（写自己的设备时参考）

1. **命令缓冲只有 1 字节也可以**：Driver 侧逐字节慢发即可（见 `cloud-path-driver-stcb` 的 `sync`）。
2. **状态帧要幂等、可重复请求**：Driver 按固定周期轮询，不会改动设备状态。
3. **事件行不要带时间戳**：时间由 Edge 打点（设备钟不可信）。
4. **响铃/电机等大电流动作会干扰 UART**：解析器要容忍损坏行，固件侧最好把 TX 让到动作之后。
5. **掉电即失真的时钟要有对时通道**：上电后等 Driver 对时，再进入正常业务。
6. **命令成功必须由设备回 ACK 定义**：写进串口不等于设备执行成功；固件对每条带 id 的
   命令回 `ACK:<id>` / `ERR:<id>:<原因>`，Driver 才敢报 SUCCEEDED。
