/** Layout SSOT: millimetres from PCB top-left, x right / z down.
 * Registered manually against schematic p1 and the assembly photo. Heights are
 * package approximations, NOT metrology. Copper artwork is extracted independently from the reference copper layers; no netlist is inferred.
 */
export const BOARD = {
  width: 92, depth: 72, thickness: 1.6, cornerRadius: 0.45, holeRadius: 1.55,
  holes: [[3.1, 3.2], [88.8, 3.2], [3.1, 68.8], [88.8, 68.8]] as const,
}
export type PackageKind = 'display' | 'qfp' | 'soic' | 'lga' | 'buzzer' | 'battery'
  | 'usb' | 'audio' | 'button' | 'navigation' | 'header' | 'hall' | 'ir-receiver'
  | 'ir-emitter' | 'ldr' | 'thermistor' | 'vibration' | 'crystal' | 'watch-crystal'
  | 'capacitor' | 'regulator' | 'led-bank'
export type Component = {
  id: string; ref: string; name: string; kind: PackageKind
  x: number; z: number; w: number; d: number; h: number
  pins?: number; marking?: string; rotation?: number; label?: boolean; description: string
  contacts?: readonly (readonly [number, number])[]
}
type Options = Partial<Pick<Component, 'pins' | 'marking' | 'rotation' | 'label' | 'contacts'>>
function c(id: string, ref: string, name: string, kind: PackageKind,
  x: number, z: number, w: number, d: number, h: number, description: string, options: Options = {}): Component {
  return { id, ref, name, kind, x, z, w, d, h, description, ...options }
}
export const COMPONENTS: readonly Component[] = [
  c('display-1', 'LED1', '左侧四位数码管', 'display', 40.5, 7.6, 29.9, 14.1, 7.4, '8 位显示的左四位。默认断电；通电演示是本地静态段码，不连接硬件。', { label: true }),
  c('display-2', 'LED2', '右侧四位数码管', 'display', 70.6, 7.6, 29.9, 14.1, 7.4, '右四位数码管；当前本地接口更新数字与横线，小数点保留断电外观。'),
  c('mcu', 'U1', 'IAP15F2K61S2', 'qfp', 54.8, 40.0, 10, 10, 1.4, 'LQFP44 主控，44 个鸥翼引脚。位置按板图与照片对齐。', { pins: 44, marking: 'STC\nIAP15F2K61S2\nLQFP44', rotation: 90, label: true }),
  c('buzzer', 'BZ', '无源蜂鸣器', 'buzzer', 27.9, 44.4, 11.7, 11.7, 7.1, '圆形无源蜂鸣器，保留声孔与极性标记；预览不会发声。', { label: true }),
  c('battery', 'BAT', 'CR1220 电池与电池座', 'battery', 23.5, 59.3, 14, 14, 3.3, 'RTC 备用纽扣电池，金属弹片和正极蚀刻独立建模。', { label: true }),
  c('usb', 'USB', 'Mini-USB 下载口', 'usb', 4.8, 56.8, 9.2, 7.6, 4.0, '开口朝左；金属壳、绝缘舌片、5 个触点分开建模。', { label: true }),
  c('audio-jack', 'PHONE', '3.5 mm 耳机座', 'audio', 7.4, 22.0, 11.3, 6.2, 4.9, '开敞式耳机座，黑色底座与外露金属触片；开口朝左。'),
  c('joystick', 'KN', '五向导航键', 'navigation', 69.8, 52.5, 6.7, 6.7, 3.0, '菱形金属壳与中央浅色摇杆，不是大型十字游戏手柄。', { rotation: 45, label: true }),
  c('button-1', 'K1', '用户按键 K1', 'button', 80.1, 66.8, 6.2, 6.2, 3.6, '底边最右侧按键，金属面板与黑色按钮。'),
  c('button-2', 'K2', '用户按键 K2', 'button', 69.1, 66.8, 6.2, 6.2, 3.6, '底边中间按键。'),
  c('button-3', 'K3', '用户按键 K3', 'button', 58.2, 66.8, 6.2, 6.2, 3.6, '底边左侧用户按键。'),
  c('reset', 'RST', '复位 / 下载键', 'button', 41.0, 52.4, 6, 6, 3.6, '蜂鸣器右下方的复位键。查看外观不会触发真实复位。'),
  c('led-row', 'L7…L0', '8 路 LED 指示灯', 'led-bank', 56.6, 22.6, 19.7, 2.0, 0.8, '实物从左至右 L7 到 L0。默认断电，通电演示可逐颗控制，支持蓝/红/绿预览；配色不代表实板 RGB 能力。'),
  c('hall', 'HALL', '霍尔传感器 A3144', 'hall', 8.9, 4.0, 4.2, 1.8, 2.4, '左上三脚磁场传感器；封装姿态按装配照片近似。'),
  c('ir-rx', 'IR_R', '红外接收头', 'ir-receiver', 15.7, 4.9, 5.0, 3.3, 3.5, '深色接收头及透镜，对应左上 IR_R 丝印。'),
  c('ir-tx', 'IR_T', '红外发射 LED', 'ir-emitter', 22.2, 5.8, 5.0, 6.8, 4.8, '透明圆顶发射 LED，两个引脚向板内弯折。'),
  c('ldr', 'Rop', '光敏电阻', 'ldr', 82.3, 27.0, 5.2, 5.2, 3.0, '陶瓷面及蛇形感光电极，位于右侧扩展口内侧。'),
  c('thermistor', 'Rt', '热敏电阻', 'thermistor', 82.4, 18.6, 2.4, 2, 3, '10K NTC 热敏电阻，保留两根金属引脚。'),
  c('vibration', 'SU', '振动 / 倒置传感器', 'vibration', 36.5, 23.4, 4.2, 12, 4.2, '黑色卧式管状器件；两脚同从下端引出，端点按装配图焊盘注册。', { contacts: [[35.5051, 31.5107], [38.0442, 31.5]] }),
  c('crystal-1', 'CY1', '12 MHz 晶体', 'crystal', 19.0, 44.6, 4.8, 10.8, 3.5, '蜂鸣器左侧的 HC-49 外形晶体。', { marking: '12.000' }),
  c('crystal-2', 'CY2', '32.768 kHz 晶体', 'watch-crystal', 40.4, 65.6, 2, 7.6, 2, '卧式金属音叉晶体，两脚同从上端引出；底侧固定焊锡不是第三根引脚。', { contacts: [[39.8215, 60.4286], [41.345, 60.4286]] }),
  c('fm', 'U10', 'FM 收音机 RDA5807FP', 'soic', 25.3, 29.6, 9.8, 4, 1.5, '实物采用双排引脚封装，不再误用 QFN。', { pins: 16, marking: 'RDA5807FP' }),
  c('ch340', 'U2', 'USB 串口 CH340G', 'soic', 10.0, 41.6, 10, 4, 1.6, 'Mini-USB 上方的 USB 转串口芯片。', { pins: 16, marking: 'CH340G' }),
  c('decoder', 'U3', '数码管译码器 74HC138', 'soic', 73.4, 23.7, 10, 4, 1.5, '位选译码器，位于 LED 排右侧。', { pins: 16, marking: '74HC138' }),
  c('uln', 'U4', '驱动阵列 ULN2003', 'soic', 72.9, 37.5, 10, 4, 1.5, '右侧中部低端驱动阵列，对应 SM 步进电机口。', { pins: 16, marking: 'ULN2003' }),
  c('rs485', 'U7', 'RS-485 收发器', 'soic', 79.7, 56.5, 5, 4, 1.5, '右下角 485 接口前的双排 8 脚芯片。', { pins: 8, marking: 'MAX485' }),
  c('ds1302', 'U5', 'RTC DS1302', 'soic', 32.5, 66.4, 5, 4, 1.5, '电池与时钟晶体之间的 RTC 芯片。', { pins: 8, marking: 'DS1302' }),
  c('eeprom', 'U6', 'EEPROM', 'soic', 48.5, 66.4, 5, 4, 1.5, '原理图 24C01 / 手册 24C02 有版本差异，不能仅凭图片断言容量。', { pins: 8, marking: '24C01/02' }),
  c('adxl', 'U9', 'ADXL345 加速度计', 'lga', 25.5, 21.0, 5, 3, 1, '左上 X/Y 丝印附近。位置为照片人工估计，非 PCB 坐标导出。', { pins: 14, marking: 'ADXL' }),
  c('regulator', 'U8', 'AS1117 3.3V 稳压器', 'regulator', 12.0, 31.6, 3.7, 6.5, 1.8, 'SOT-223 外形，散热片及三个引脚。'),
  c('cap-c3', 'C3', '电解电容 C3', 'capacitor', 13.4, 65.8, 6.3, 6.3, 5.4, '照片顶面可辨 100，原理图第 6 页 C3 为 220 µF，存在装配差异；外观取照片，不据此判定实板 BOM 或耐压。'),
  c('header-ext', 'EXT', '4 针扩展口', 'header', 92.1, 23.8, 4.5, 10.16, 5, 'GND / P1.0 / P1.1 / 5V；实物为直角母座，插孔朝向板外。', { pins: 4 }),
  c('header-sm', 'SM', '5 针步进电机口', 'header', 92.1, 41.0, 4.5, 12.7, 5, '原理图 CON5：5V / S1 / S2 / S3 / S4，不是 6 针舵机口；实物为直角母座。', { pins: 5 }),
  c('header-485', '485', '2 针 RS-485 口', 'header', 92.1, 55.2, 4.5, 5.08, 5, 'A / B 两针，位于右下边；实物为直角母座。', { pins: 2 }),
]
/** Manual registration frame of the front assembly drawing, normalized to 1786×1408.
 * PCB rectangle: (66,64)..(1722,1344). These are reference-image anchors, not PCB CAD data.
 * Unlike a perspective photo, this frame does not shift elevated packages against the PCB.
 */
export function artworkPoint(x: number, y: number): [number, number] {
  return [(x - 66) * BOARD.width / 1656, (y - 64) * BOARD.depth / 1280]
}
export type Passive = {
  x: number; z: number; ref: string; kind: 'resistor' | 'capacitor' | 'led' | 'diode' | 'transistor'
  vertical: boolean; marking?: string
}
/** Conservative XY contact envelopes in mm, including the actual discrete package leads. */
export const PASSIVE_ENVELOPES = {
  resistor: { w: 2.24, d: 0.92 }, capacitor: { w: 2.24, d: 0.92 }, led: { w: 2.24, d: 0.92 },
  diode: { w: 4.3, d: 1.35 }, transistor: { w: 3.2, d: 2.2 },
} as const
// Only these codes are legible in the assembled-board photo; no guessed BOM markings.
const PHOTO_MARKINGS: Readonly<Record<string, string>> = {
  R17: '101', R18: '101', R15: '101', R16: '101', R19: '101', R20: '101', R22: '101', R21: '101',
  R49: '201', R54: '101',
}
function p(ref: string, x: number, y: number, vertical = false, kind: Passive['kind'] = 'resistor'): Passive {
  const [bx, bz] = artworkPoint(x, y)
  return { x: bx, z: bz, ref, kind, vertical, marking: PHOTO_MARKINGS[ref] }
}
/** Individually identified bodies. Silk comes from the extracted reference layer, not repeated here. No synthetic R17..R24 banks. */
export const PASSIVES: readonly Passive[] = [
  p('R48', 226, 200, false, 'resistor'), p('R49', 265, 234, false, 'resistor'),
  p('C11', 309, 216, false, 'capacitor'), p('C6', 374, 216, false, 'capacitor'),
  p('R54', 374, 247, false, 'resistor'),
  p('LC2', 229, 280, false, 'capacitor'), p('LC1', 229, 314, false, 'capacitor'),
  p('C20', 295, 286, false, 'capacitor'), p('C13', 295, 324, false, 'capacitor'),
  p('R29', 363, 361, true, 'resistor'),
  p('R38', 409, 361, true, 'resistor'),
  p('R39', 455, 361, true, 'resistor'),
  p('C2', 602, 349, false, 'capacitor'),
  p('R53', 661, 395, true, 'resistor'),
  p('R55', 661, 468, true, 'resistor'),
  p('R58', 661, 539, true, 'resistor'),
  p('GP1', 363, 429, true, 'led'), p('GP2', 409, 429, true, 'led'),
  p('GP3', 455, 429, true, 'led'),
  p('C23', 377, 533, true, 'capacitor'), p('C22', 379, 649, true, 'capacitor'),
  p('C5', 169, 602, true, 'capacitor'), p('C7', 169, 670, true, 'capacitor'),
  p('C17', 432, 715, false, 'capacitor'), p('R60', 512, 715, false, 'resistor'),
  p('C12', 602, 715, false, 'capacitor'),
  p('C15', 204, 934, false, 'capacitor'), p('C16', 307, 944, true, 'capacitor'),
  p('R52', 320, 1028, false, 'resistor'),
  p('R30', 716, 670, false, 'resistor'), p('R4', 716, 705, false, 'resistor'),
  p('R9', 716, 739, false, 'resistor'), p('R8', 716, 775, false, 'resistor'),
  p('R1', 716, 808, false, 'resistor'), p('R2', 716, 842, false, 'resistor'),
  p('R14', 716, 877, false, 'resistor'), p('C21', 693, 958, false, 'capacitor'),
  p('D1', 684, 1001, false, 'diode'),
  p('VCC', 815, 650, false, 'led'), p('TX2', 815, 718, false, 'led'), p('RX2', 815, 762, false, 'led'),
  p('TX', 815, 831, false, 'led'), p('RX', 815, 878, false, 'led'),
  p('C10', 939, 937, false, 'capacitor'), p('C1', 1035, 961, false, 'capacitor'),
  p('C4', 1035, 994, false, 'capacitor'),
  p('Q1', 945, 999, false, 'transistor'),
  p('R56', 1550, 442, false, 'resistor'), p('C14', 1247, 508, true, 'capacitor'),
  p('R51', 1516, 647, false, 'resistor'), p('R50', 1516, 681, false, 'resistor'),
  p('R57', 1550, 617, false, 'resistor'),
  p('C19', 1580, 650, false, 'capacitor'), p('C18', 1580, 685, false, 'capacitor'),
  p('R23', 1481, 851, false, 'resistor'), p('R26', 1469, 885, false, 'resistor'),
  p('R11', 1517, 900, false, 'resistor'), p('R10', 1517, 936, false, 'resistor'),
  p('R12', 1517, 972, false, 'resistor'),
  p('R28', 1582, 1000, false, 'resistor'),
  p('C8', 1415, 1118, true, 'capacitor'),
  p('R13', 1570, 1070, false, 'resistor'),
  p('R27', 1596, 1127, true, 'resistor'),
  ...['R17', 'R18', 'R15', 'R16', 'R19', 'R20', 'R22', 'R21'].map((ref, i) => p(ref, 900 + i * 45.7, 533, true, 'resistor')),
  p('R59', 798, 442, true, 'resistor'), p('R42', 851, 442, true, 'resistor'),
  ...['R43', 'R44', 'R45'].map((ref, i) => p(ref, 644 + i * 45, 1116, true, 'resistor')),
  ...['R46', 'R47', 'R31', 'R32', 'R33', 'R34', 'R35', 'R36'].map((ref, i) => p(ref, 922 + i * 46, 1116, true, 'resistor')),
  ...['S1', 'S2', 'S3', 'S4', 'DR', 'RX1', 'TX1'].map((ref, i) => p(ref, 1585, 738 + i * 35, false, 'led')),
  p('R3', 865, 1127, true, 'resistor'),
  p('R5', 1016, 1207, true, 'resistor'), p('R37', 1016, 1277, true, 'resistor'),
  p('R6', 1209, 1207, true, 'resistor'), p('R40', 1209, 1277, true, 'resistor'),
  p('R7', 1400, 1207, true, 'resistor'), p('R41', 1400, 1277, true, 'resistor'),
  p('C9', 570, 1277, true, 'capacitor'),
]
export function worldPosition(x: number, z: number): [number, number, number] {
  return [x - BOARD.width / 2, BOARD.thickness, z - BOARD.depth / 2]
}
export function boardUV(x: number, z: number): [number, number] {
  return [(x + BOARD.width / 2) / BOARD.width, 1 - (z + BOARD.depth / 2) / BOARD.depth]
}
