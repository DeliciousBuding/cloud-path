import { describe, expect, it } from 'vitest'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin } from '../device-twin'

const device: DeviceView = {
  id: 'edge-1/board-1',
  edge_id: 'edge-1',
  adapter: 'stcb',
  online: true,
  state: {},
  updated_at: 1,
  last_seen: 1,
}

const descriptor: DeviceDescriptor = {
  device_id: 'board-1',
  external_id: 'COM3',
  status: 'online',
  entities: [
    {
      entity_id: 'led-bank',
      unique_key: 'led-bank',
      category: 'actuator',
      capabilities: ['cloudpath.dev/capability/led@1'],
      observations: {
        mask: { capability: 'cloudpath.dev/capability/led@1', property: 'mask', value: 42 },
      },
    },
    {
      entity_id: 'display',
      unique_key: 'display',
      category: 'actuator',
      capabilities: ['cloudpath.dev/capability/display-text@1'],
      observations: {
        mode: { capability: 'cloudpath.dev/capability/display-text@1', property: 'mode', value: 'clock' },
        page: { capability: 'cloudpath.dev/capability/display-text@1', property: 'page', value: 'date' },
      },
    },
  ],
}

describe('device twin resolution', () => {
  it('maps descriptor observations to the STC-B renderer without inventing display glyphs', () => {
    const resolution = resolveDeviceTwin(device, descriptor)
    expect(resolution?.visualState).toEqual({
      powered: true,
      display: '        ',
      ledMask: 42,
      ledColor: 'blue',
    })
    expect(resolution?.indicators).toEqual([
      { kind: 'led', value: '0x2A', tone: 'good' },
      { kind: 'mode', value: 'clock', tone: 'good' },
      { kind: 'page', value: 'date', tone: 'good' },
    ])
  })

  it('fails closed for adapters without an installed frontend model', () => {
    expect(resolveDeviceTwin({ ...device, adapter: 'demo' }, descriptor)).toBeUndefined()
  })
})
