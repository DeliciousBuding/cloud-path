#!/usr/bin/env python3
"""可复现构建 webui 自托管中文子集字体（NotoSansSC-Variable.woff2）。

来源与授权：Noto Sans SC 可变字体（SIL OFL），源 TTF 取自 google/fonts 仓库
  https://github.com/google/fonts/raw/main/ofl/notosanssc/NotoSansSC%5Bwght%5D.ttf
字符集策略：GB2312 一级字表（3755 汉字，覆盖 99.9% 人名/设备名/日常词）
  ∪ webui/src 界面词汇 ∪ 中文标点/全角/常用符号；保留可变字重轴（100-900）。
产物：NotoSansSC-subset.woff2（约 1.05MB），人工拷贝到
  webui/src/assets/fonts/NotoSansSC-Variable.woff2 后由 index.css @font-face 引用
（unicode-range 只接管 CJK，拉丁/数字仍走 Geist）。
依赖：pip install fonttools brotli
"""
import io, glob, os, sys
SRC_TTF = os.path.join(os.path.dirname(__file__), '..', '..', '.tmp', 'NotoSansSC-var.ttf')
OUT = os.path.join(os.path.dirname(__file__), 'NotoSansSC-subset.woff2')

from fontTools import subset as ftsubset

# GB2312 一级字表（3755 汉字，覆盖 99.9% 人名/设备名/日常词）
l1 = set()
for hi in range(0xB0, 0xD8):
    for lo in range(0xA1, 0xFF):
        b = bytes([hi, lo])
        try:
            ch = b.decode('gb2312')
        except Exception:
            continue
        if '\u4e00' <= ch <= '\u9fff':
            l1.add(ch)
print('gb2312-L1 hanzi:', len(l1))

# UI 源码汉字表（含运行时可能回显的词汇）
vocab = set()
root = os.path.join(os.path.dirname(__file__), '..', 'src')
for f in glob.glob(root + '/**/*.tsx', recursive=True) + glob.glob(root + '/**/*.ts', recursive=True):
    for ch in io.open(f, encoding='utf-8').read():
        if '\u4e00' <= ch <= '\u9fff':
            vocab.add(ch)
extra_vocab = vocab - l1
print('src vocab:', len(vocab), 'outside L1:', len(extra_vocab), ''.join(sorted(extra_vocab))[:60])

punct = set('、。〈〉《》「」『』【】〖〗〔〘〙〚〛！＂＃￥％＆＇（）＊＋，－．／：；＜＝＞？＠［＼］＾＿｀｛｜｝～｟｠〃〄〆〇〜〝〞〟·—…～｜①')
punct |= {chr(c) for c in range(0x3000, 0x3040)}      # CJK 标点符号
punct |= {chr(c) for c in range(0xFF01, 0xFF5F)}      # 全角形
punct |= set('°±×÷℃‰′″←↑→↓↔§¶†‡•‧‰∞∑∏√≤≥≠≈±−–—―‘’“”‥…‧・')
chars = l1 | vocab | punct
uni = ','.join('U+%04X' % ord(c) for c in sorted(chars))
print('total codepoints:', len(chars))

args = [
    SRC_TTF, '--output-file=' + OUT, '--flavor=woff2',
    '--unicodes=' + uni, '--layout-features=*', '--no-hinting', '--desubroutinize',
    '--name-IDs=1,2', '--notdef-outline', '--drop-tables+=DSIG',
]
ftsubset.main(args)
import os
print('woff2 bytes:', os.path.getsize(OUT))
