import { useState } from 'react'
import { fireEvent, render, screen, waitFor } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { afterEach, describe, expect, it, vi } from 'vitest'
import { ConfirmDialog } from '@/components/ConfirmDialog'

function DialogHarness({ busy = false, onCancel = () => {}, onConfirm = () => {} }: { busy?: boolean; onCancel?: () => void; onConfirm?: () => void }) {
  const [open, setOpen] = useState(false)
  return <div style={{ transform: 'translateY(0)' }}>
    <button onClick={() => setOpen(true)}>打开确认</button>
    <button>背景操作</button>
    <ConfirmDialog open={open} busy={busy} title="确认修改？" body="将修改当前设备的输出。"
      requireAck="我已确认操作目标" confirmLabel="执行修改"
      onCancel={() => { onCancel(); setOpen(false) }} onConfirm={onConfirm} />
  </div>
}

afterEach(() => { document.body.style.overflow = '' })

describe('确认框的视口与键盘边界', () => {
  it('portal 离开变换父容器，覆盖视口而不是局部页面', async () => {
    const user = userEvent.setup()
    const { container } = render(<DialogHarness />)
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    const dialog = screen.getByRole('dialog', { name: '确认修改？' })
    expect(container).not.toContainElement(dialog)
    expect(document.body).toContainElement(dialog)
    expect(dialog).toHaveAccessibleDescription('将修改当前设备的输出。')
    expect(document.body).toHaveAttribute('data-scroll-locked', '1')
    expect(screen.getByRole('button', { name: '取消' })).toHaveFocus()
  })

  it('Tab / Shift+Tab 在当前可用控件内循环，不能跑到背景', async () => {
    const user = userEvent.setup()
    render(<DialogHarness />)
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    const ack = screen.getByRole('checkbox')
    const cancel = screen.getByRole('button', { name: '取消' })
    const confirm = screen.getByRole('button', { name: '执行修改' })
    expect(confirm).toBeDisabled()
    await user.tab()
    expect(ack).toHaveFocus()
    await user.tab({ shift: true })
    expect(cancel).toHaveFocus()
    await user.click(ack)
    await user.tab({ shift: true })
    expect(confirm).toHaveFocus()
    await user.tab()
    expect(ack).toHaveFocus()
  })

  it('Esc 取消并恢复触发焦点和原来的背景滚动状态', async () => {
    const user = userEvent.setup()
    const cancel = vi.fn()
    document.body.style.overflow = 'auto'
    render(<DialogHarness onCancel={cancel} />)
    const trigger = screen.getByRole('button', { name: '打开确认' })
    await user.click(trigger)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(cancel).toHaveBeenCalledOnce()
    expect(screen.queryByRole('dialog')).not.toBeInTheDocument()
    await waitFor(() => expect(trigger).toHaveFocus())
    expect(document.body.style.overflow).toBe('auto')
  })

  it('点击对话框内容不关闭，点击遮罩是取消而不是确认', async () => {
    const user = userEvent.setup()
    const cancel = vi.fn(), confirm = vi.fn()
    render(<DialogHarness onCancel={cancel} onConfirm={confirm} />)
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    const dialog = screen.getByRole('dialog')
    fireEvent.mouseDown(dialog)
    expect(cancel).not.toHaveBeenCalled()
    const overlay = document.querySelector('.dialog-backdrop')
    expect(overlay).not.toBeNull()
    await user.click(overlay as HTMLElement)
    await waitFor(() => expect(cancel).toHaveBeenCalledOnce())
    expect(confirm).not.toHaveBeenCalled()
  })

  it('busy 时 Esc 与遮罩都不能关闭，回到可操作状态后仍能取消', async () => {
    const user = userEvent.setup()
    const cancel = vi.fn()
    const view = render(<DialogHarness busy onCancel={cancel} />)
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    expect(screen.getByRole('dialog')).toHaveFocus()
    fireEvent.keyDown(document, { key: 'Escape' })
    await user.click(document.querySelector('.dialog-backdrop') as HTMLElement)
    expect(cancel).not.toHaveBeenCalled()
    view.rerender(<DialogHarness onCancel={cancel} />)
    fireEvent.keyDown(document, { key: 'Escape' })
    expect(cancel).toHaveBeenCalledOnce()
  })

  it('重新打开不复用上一次的已勾选确认', async () => {
    const user = userEvent.setup()
    render(<DialogHarness />)
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    await user.click(screen.getByRole('checkbox'))
    expect(screen.getByRole('button', { name: '执行修改' })).toBeEnabled()
    await user.click(screen.getByRole('button', { name: '取消' }))
    await user.click(screen.getByRole('button', { name: '打开确认' }))
    expect(screen.getByRole('checkbox')).not.toBeChecked()
    expect(screen.getByRole('button', { name: '执行修改' })).toBeDisabled()
  })
})
