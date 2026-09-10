import { screen } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it, vi } from 'vitest'
import { DeviceBindingSelector } from '@/components/plugin/DeviceBindingSelector'
import { installFetch, stubResponse } from '@/test/http'
import { renderWithProviders } from '@/test/render'

describe('DeviceBindingSelector', () => {
  it('selects an explicit device for a requirement', async () => {
    installFetch((url) => {
      if (url === '/api/plugin-instances/music-room/bindings') {
        return stubResponse(200, {
          instance_id: 'music-room', running: true,
          bindings: [{ requirement_id: 'sound', capability: 'cloudpath.dev/capability/buzzer@1', entity_id: 'buzzer', device_id: 'edge/board-1' }],
          requirements: [{ id: 'sound', capability: 'cloudpath.dev/capability/buzzer@1', cardinality: 'one' }],
          candidates: [
            { entity_id: 'buzzer', device_id: 'edge/board-1', name: '蜂鸣器', capabilities: ['cloudpath.dev/capability/buzzer@1'] },
            { entity_id: 'buzzer', device_id: 'edge/board-2', name: '蜂鸣器', capabilities: ['cloudpath.dev/capability/buzzer@1'] },
          ],
        })
      }
      return stubResponse(404)
    })
    const onChange = vi.fn()
    renderWithProviders(<DeviceBindingSelector instanceId="music-room" onChange={onChange} />)

    await screen.findByText('蜂鸣器 · edge/board-2')
    await userEvent.click(screen.getAllByRole('checkbox')[1])
    expect(onChange).toHaveBeenLastCalledWith(JSON.stringify([
      { requirement_id: 'sound', entity_id: 'buzzer', device_id: 'edge/board-2' },
    ]))
  })

  it('keeps saved targets editable while stopped', async () => {
    installFetch((url) => {
      if (url === '/api/plugin-instances/music-room/bindings') {
        return stubResponse(200, {
          instance_id: 'music-room', running: false,
          bindings: [{ requirement_id: 'sound', capability: 'cloudpath.dev/capability/buzzer@1', entity_id: 'buzzer' }],
          requirements: [{ id: 'sound', capability: 'cloudpath.dev/capability/buzzer@1', cardinality: 'one' }],
          candidates: [
            { entity_id: 'buzzer', device_id: 'edge/board-1', name: '蜂鸣器', capabilities: ['cloudpath.dev/capability/buzzer@1'] },
            { entity_id: 'buzzer', device_id: 'edge/board-2', name: '蜂鸣器', capabilities: ['cloudpath.dev/capability/buzzer@1'] },
          ],
        })
      }
      return stubResponse(404)
    })
    renderWithProviders(<DeviceBindingSelector instanceId="music-room" value={JSON.stringify([{ requirement_id: 'sound', entity_id: 'buzzer' }])} onChange={() => undefined} />)

    expect(await screen.findByText('应用当前未运行；下面仍可修正已保存的设备目标。')).toBeInTheDocument()
    expect(screen.getByText('蜂鸣器 · edge/board-2')).toBeInTheDocument()
  })
})
