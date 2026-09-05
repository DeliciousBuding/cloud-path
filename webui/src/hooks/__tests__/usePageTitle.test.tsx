// 标签标题是路由级契约：多标签运维场景下靠它区分标签页，故锁死格式与回落链。
import { render } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { usePageTitle } from '@/hooks/usePageTitle'

function Probe({ page }: { page: string }) {
  usePageTitle(page)
  return null
}

describe('usePageTitle', () => {
  it('页面名带品牌后缀写入标签标题', () => {
    render(<Probe page="概览" />)
    expect(document.title).toBe('概览 · Cloudpath')
  })

  it('路由切换时标题跟随更新，不残留上一页', () => {
    const { rerender } = render(<Probe page="设备" />)
    expect(document.title).toBe('设备 · Cloudpath')
    rerender(<Probe page="活动" />)
    expect(document.title).toBe('活动 · Cloudpath')
  })

  it('详情页对象未加载时回落到通用页面名，不出现 undefined', () => {
    const d = undefined as { name?: string; id: string } | undefined
    render(<Probe page={d ? d.name || d.id : '设备'} />)
    expect(document.title).toBe('设备 · Cloudpath')
    expect(document.title).not.toContain('undefined')
  })
})
