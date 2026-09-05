import { screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { EventFeed } from '@/components/EventFeed'
import { renderWithProviders } from '@/test/render'
import type { EventView } from '@/lib/types'

const ev = (id: number, ts: number): EventView => ({ id, ts, type: 'device-booted', device_id: 'e/d', payload: '' })

describe('EventFeed day 分组', () => {
  const now = Math.floor(Date.now() / 1000)

  it('dayGrouped（跨天历史）按天分组，组头是扫读锚点', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} dayGrouped limit={10} />)
    expect(screen.getByText('今天')).toBeInTheDocument()
    expect(screen.getByText('昨天')).toBeInTheDocument()
  })

  it('日期由组头承载：行内不再重复完整日期（完整时间只在 title）', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} dayGrouped limit={10} />)
    expect(screen.queryByText(/\d{4}\/\d+\/\d+ /)).toBeNull()
  })

  it('紧凑列表（概览/详情页）不插组头，保持单行密度', () => {
    renderWithProviders(<EventFeed events={[ev(1, now), ev(2, now - 86_400)]} limit={10} />)
    expect(screen.queryByText('今天')).toBeNull()
    expect(screen.queryByText('昨天')).toBeNull()
  })
})
