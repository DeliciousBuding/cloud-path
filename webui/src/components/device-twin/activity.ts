import { create } from 'zustand'
import type { Tone } from '@/components/ui'

export type DeviceTwinActivity = {
  id: string
  deviceId: string
  label: string
  entityID?: string
  eventType?: string
  detail?: string
  tone: Tone
  at: number
}

interface DeviceTwinActivityState {
  byDevice: Record<string, DeviceTwinActivity | undefined>
}

/** Short-lived UI feedback for a device twin. Hardware truth remains in DeviceView. */
export const useDeviceTwinActivity = create<DeviceTwinActivityState>(() => ({ byDevice: {} }))

export function reportDeviceTwinActivity(activity: Omit<DeviceTwinActivity, 'at'> & { at?: number }) {
  const next = { ...activity, at: activity.at ?? Date.now() / 1000 }
  useDeviceTwinActivity.setState((state) => ({
    byDevice: { ...state.byDevice, [next.deviceId]: next },
  }))
}
