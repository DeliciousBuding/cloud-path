# CloudPath 命名词表

最后更新：2026-09-09

本文定义 CloudPath 面向用户的中文名称，以及它们与协议、代码中 canonical 机器标识的对应关系。
协议字段、JSON/YAML key、数据库列、plugin ID、capability ID、路由和机器标识保持稳定；
界面文案不反向改机器契约。

## 用户侧词表

| 对象 | 界面统一用词 | 机器标识 | 边界 |
|---|---|---|---|
| Edge | **网关** | Edge / `edge_id` | 主界面不再写“边缘节点”或裸露 Edge；架构文档可保留 Edge |
| Server | **中心服务** | Server / `server` | 与网关侧的宿主区分 |
| Device | **设备** | Device / `device_id` | 保持 |
| Entity | **实体** | Entity / `entity_id` | 不再写“设备对象” |
| Capability | **能力** | Capability / capability URI | 不再写“设备功能”“功能标识” |
| Observation | **状态** | Observation | “观测”只用于高级/诊断语境 |
| Event | **事件** | Event / event type | 历史列表统一叫“事件记录” |
| Command | **操作** | Command / `cmd` | 历史列表叫“操作记录”；“命令”只保留在协议/诊断 |
| Application | **应用**（首次可写“应用插件”） | Application | 业务插件 |
| Driver | **驱动**（首次可写“驱动插件”） | Driver | 设备接入插件 |
| Connector | **连接器** | Connector | 连接器不创建运行实例 |
| Plugin | **插件** | Plugin | 指可安装包，不指一次运行 |
| Plugin Instance | **运行实例**（短标签“实例”） | Plugin Instance / `instance_id` | 不再写“项目”“应用实例”“插件实例” |
| Binding | **绑定** | Binding | 应用与设备能力之间的绑定 |
| Edge Agent | **网关** | Edge / `edge_id` | 用户侧不需要“代理”这个实现词 |

“运行位置”的取值只有两种：**中心服务**或**网关 <id>**。不要写成“节点”。

## 使用规则

1. 主界面优先使用本表“界面统一用词”；机器 ID、Capability URI、raw JSON 只在高级、诊断或悬停详情出现。
2. 协议字段、JSON/YAML key、数据库字段、plugin ID、capability ID、路由不翻译、不重命名。
3. 架构和协议文档可以继续使用 Device / Entity / Capability / Observation / Event / Command 等英文对象名；
   面向用户的正文、按钮、标题和错误提示必须使用本表中文词。
4. 同一个概念只用一个词。不要在同一个页面随机混用“能力/功能”“事件/状态记录”“操作/命令”“实例/项目”。
5. 新 Driver、Application、Connector 的展示文案也遵守本表；设备专属名称仍由插件声明，Core 不硬编码硬件语义。

## 禁用词与替代

| 不要用 | 用 |
|---|---|
| 边缘节点 / Edge（主界面） | 网关 |
| 设备对象 | 实体 |
| 设备功能 / 功能标识 | 能力 / 能力标识 |
| 状态记录（指 Event） | 事件记录 |
| 状态事件 | 事件 |
| 命令（指用户下发的动作） | 操作 |
| 项目 | 运行实例 / 实例 |
| 应用实例 / 插件实例 | 运行实例 |
| 应用参数 / 应用设置（泛指插件） | 插件参数 / 插件设置 |
| 节点 ID | 网关 ID |
