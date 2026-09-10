import { describe, expect, it } from 'vitest'
import type { DeviceDescriptor, DeviceView } from '@/lib/types'
import { resolveDeviceTwin } from '../device-twin'

const device: DeviceView = {
  id: 'edge-1/board-1',
  edge_id: 'edge-1',
  adapter: 'stcb',
  online: true,
  state: {},
  updated_at: 1_000,
  last_seen: 1_000,
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
      entity_id: 'clock',
      unique_key: 'clock',
      category: 'sensor',
      capabilities: ['cloudpath.dev/capability/clock@1'],
      observations: {
        time: { capability: 'cloudpath.dev/capability/clock@1', property: 'time', value: '12:34:05' },
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
  it('maps descriptor observations and derives only the reported clock face', () => {
    const resolution = resolveDeviceTwin(device, descriptor, 1_010)
    expect(resolution?.visualState).toEqual({
      powered: true,
      display: '12-34-15',
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
