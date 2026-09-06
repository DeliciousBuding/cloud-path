import { render, screen } from '@testing-library/react'
import { describe, expect, it } from 'vitest'
import { MemoryRouter, Route, Routes, useLocation } from 'react-router'
import LegacyDevicePanelRedirect from '@/pages/Pillbox'

function Destination() {
  const location = useLocation()
  return <output aria-label="目标位置">{location.pathname + location.search}</output>
}

function open(path: string) {
  return render(<MemoryRouter initialEntries={[path]}><Routes>
    <Route path="/pillbox" element={<LegacyDevicePanelRedirect />} />
    <Route path="/pillbox/:edgeId/:deviceId" element={<LegacyDevicePanelRedirect />} />
    <Route path="/devices" element={<Destination />} />
    <Route path="/devices/:edgeId/:deviceId" element={<Destination />} />
  </Routes></MemoryRouter>)
}

describe('旧设备面板入口兼容', () => {
  it('无设备的旧入口去设备列表，不再伪造业务面板', async () => {
    open('/pillbox')
    expect(await screen.findByLabelText('目标位置')).toHaveTextContent('/devices')
    expect(screen.queryByText('提醒与漏服')).not.toBeInTheDocument()
  })
  it('指定设备的旧入口直达该设备控制分区', async () => {
    open('/pillbox/edge-one/device-nine')
    expect(await screen.findByLabelText('目标位置')).toHaveTextContent('/devices/edge-one/device-nine?tab=controls')
  })
  it('保留编码后的设备标识，不拼接成其它路由', async () => {
    open('/pillbox/edge-one/device%20nine')
    expect(await screen.findByLabelText('目标位置')).toHaveTextContent('/devices/edge-one/device%20nine?tab=controls')
  })
})
