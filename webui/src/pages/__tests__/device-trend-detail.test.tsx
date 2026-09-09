import { screen } from '@testing-library/react'
import { Route, Routes } from 'react-router'
import { beforeEach, describe, expect, it } from 'vitest'
import DeviceTrendDetail from '@/pages/DeviceTrendDetail'
import { installFetch, stubResponse } from '@/test/http'
import { makeDeviceView } from '@/test/fixtures'
import { renderWithProviders, resetStores } from '@/test/render'

const ROUTE = '/devices/edge-1/dev-9/trends/temperature'

beforeEach(() => { resetStores() })

describe('趋势详情：历史读取与设备在线状态解耦', () => {
  it('设备离线仍打开已保存采样，并展示明细', async () => {
    const http = installFetch((url) => {
      if (url === '/api/devices/edge-1/dev-9') {
        return stubResponse(200, makeDeviceView({ online: false }))
      }
      if (url.includes('/api/devices/edge-1/dev-9/samples')) {
        return stubResponse(200, {
          samples: [{ device_id: 'edge-1/dev-9', key: 'temperature', ts: 1_780_000_000, value: 25.5, quality: 'good' }],
        })
      }
      if (url === '/api/descriptors' || url === '/api/capabilities' || url.endsWith('/descriptor')) {
        return stubResponse(404, {})
      }
      return stubResponse(404, {})
    })
    renderWithProviders(
      <Routes><Route path="/devices/:edgeId/:deviceId/trends/:seriesKey" element={<DeviceTrendDetail />} /></Routes>,
      ROUTE,
    )

    expect(await screen.findByRole('heading', { level: 1, name: 'Temperature' })).toBeInTheDocument()
    expect(screen.getByText('设备当前离线；这里仍可查看服务器已保存的历史采样。')).toBeInTheDocument()
    expect(screen.getAllByText('25.5')).toHaveLength(5)
    expect(screen.getByText('良好')).toBeInTheDocument()
    expect(http.to('/samples?key=temperature')).toHaveLength(1)
  })
})
