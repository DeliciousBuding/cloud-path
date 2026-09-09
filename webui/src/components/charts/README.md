# Charts

通用图表层只负责“如何画”，不负责“数据从哪里来”或“字段是什么意思”。

- `TimeSeriesChart`：单序列时间图，支持 area/line/bar、零基线、隐藏 Y 轴、Tooltip 与 Brush 缩放。
- `Sparkline`：纯 SVG 迷你趋势线。
- `chart-utils`：统计、按时间合并去重、时间窗裁剪等纯函数。
- `ChartPoint` 是唯一输入形状：`{ t: unixSeconds, v: number }`。

边界：这里不认识设备、Descriptor、Capability、WebSocket 或 REST；业务页面负责把历史、实时和声明元数据归一化成 `ChartPoint`。


只使用 `Sparkline` 的调用方直接 import `@/components/charts/Sparkline`，不要经 barrel 引入 `TimeSeriesChart`，避免把 Recharts 拖进不需要图表的懒加载 chunk。
