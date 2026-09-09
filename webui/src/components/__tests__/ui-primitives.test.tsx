import { render, screen } from '@testing-library/react'
import { MemoryRouter } from 'react-router'
import { describe, expect, it } from 'vitest'
import { Button, ButtonLink, Checkbox, IconButton, Input, Radio, Textarea } from '@/components/ui'

describe('UI primitives', () => {
  it('maps Button variant, size and loading state to the design system contract', () => {
    render(<Button variant="danger" size="sm" loading>删除</Button>)
    const button = screen.getByRole('button', { name: '删除' })
    expect(button).toHaveClass('btn', 'btn-danger', 'btn-sm')
    expect(button).toBeDisabled()
    expect(button).toHaveAttribute('aria-busy', 'true')
  })

  it('keeps icon-only actions accessible', () => {
    render(<IconButton label="关闭">×</IconButton>)
    const button = screen.getByRole('button', { name: '关闭' })
    expect(button).toHaveClass('btn', 'btn-bare', 'btn-icon')
  })

  it('routes form controls through the tokenized primitives', () => {
    render(<>
      <Input aria-label="名称" compact />
      <Textarea aria-label="说明" />
      <Checkbox aria-label="启用" />
      <Radio aria-label="选择" />
    </>)
    expect(screen.getByRole('textbox', { name: '名称' })).toHaveClass('input', 'input-sm')
    expect(screen.getByRole('textbox', { name: '说明' })).toHaveClass('input')
    expect(screen.getByRole('checkbox', { name: '启用' })).toHaveClass('checkbox')
    expect(screen.getByRole('radio', { name: '选择' })).toHaveClass('radio')
  })

  it('uses link semantics for navigation actions', () => {
    render(<MemoryRouter><ButtonLink to="/devices" variant="ghost">设备</ButtonLink></MemoryRouter>)
    const link = screen.getByRole('link', { name: '设备' })
    expect(link).toHaveClass('btn', 'btn-ghost')
    expect(link).toHaveAttribute('href', '/devices')
  })
})
