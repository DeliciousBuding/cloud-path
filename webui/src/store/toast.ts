// 轻提示：命令回执/错误反馈。固定右下，毛玻璃卡片，自动消失。
import { create } from 'zustand'

export type ToastTone = 'ok' | 'bad' | 'info'

export interface ToastItem {
  id: number
  title: string
  detail?: string
  tone: ToastTone
}

interface ToastState {
  items: ToastItem[]
  push: (t: Omit<ToastItem, 'id'>) => void
  dismiss: (id: number) => void
}

let toastId = 1

export const useToasts = create<ToastState>((set) => ({
  items: [],
  push: (t) => {
    const id = toastId++
    set((s) => ({ items: [...s.items.slice(-4), { ...t, id }] }))
    setTimeout(() => set((s) => ({ items: s.items.filter((x) => x.id !== id) })), 3200)
  },
  dismiss: (id) => set((s) => ({ items: s.items.filter((x) => x.id !== id) })),
}))

export const toast = {
  ok: (title: string, detail?: string) => useToasts.getState().push({ title, detail, tone: 'ok' }),
  bad: (title: string, detail?: string) => useToasts.getState().push({ title, detail, tone: 'bad' }),
  info: (title: string, detail?: string) => useToasts.getState().push({ title, detail, tone: 'info' }),
}