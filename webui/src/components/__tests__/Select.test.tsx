import { useState } from 'react'
import { fireEvent, render, screen, within } from '@testing-library/react'
import userEvent from '@testing-library/user-event'
import { describe, expect, it } from 'vitest'
import { Select } from '@/components/ui'

function ControlledSelect({ disabled = false }: { disabled?: boolean }) {
  const [value, setValue] = useState('a')
  return (
    <Select aria-label="示例" value={value} disabled={disabled} onChange={(event) => setValue(event.target.value)}>
      <optgroup label="常用">
        <option value="a">选项 A</option>
        <option value="b">选项 B</option>
      </optgroup>
      <option value="c">选项 C</option>
    </Select>
  )
}

describe('Select primitive', () => {
  it('renders a tokenized listbox and keeps the native select as the semantic layer', async () => {
    const user = userEvent.setup()
    const { container } = render(<ControlledSelect />)
    const native = screen.getByRole('combobox', { name: '示例' })
    expect(native).toHaveClass('sr-only')
    expect(native).toHaveValue('a')

    await user.click(container.querySelector('.select-trigger') as HTMLElement)
    const listbox = screen.getByRole('listbox', { name: '示例' })
    expect(listbox).toBeInTheDocument()
    expect(within(listbox).getByRole('option', { name: '选项 A' })).toHaveAttribute('aria-selected', 'true')
    expect(within(listbox).getByRole('option', { name: '选项 B' })).toBeInTheDocument()
    expect(within(listbox).getByText('常用')).toBeInTheDocument()
  })

  it('selects an option by pointer and updates both visible and native values', async () => {
    const user = userEvent.setup()
    const { container } = render(<ControlledSelect />)
    const trigger = container.querySelector('.select-trigger') as HTMLElement

    await user.click(trigger)
    await user.click(within(screen.getByRole('listbox', { name: '示例' })).getByRole('option', { name: '选项 B' }))

    expect(trigger).toHaveTextContent('选项 B')
    expect(screen.getByRole('combobox', { name: '示例' })).toHaveValue('b')
  })

  it('supports keyboard navigation and escape', async () => {
    render(<ControlledSelect />)
    const native = screen.getByRole('combobox', { name: '示例' })
    native.focus()

    fireEvent.keyDown(native, { key: 'ArrowDown' })
    expect(await screen.findByRole('listbox', { name: '示例' })).toBeInTheDocument()

    fireEvent.keyDown(native, { key: 'ArrowDown' })
    fireEvent.keyDown(native, { key: 'Enter' })
    expect(native).toHaveValue('b')

    fireEvent.keyDown(native, { key: 'ArrowDown' })
    expect(await screen.findByRole('listbox', { name: '示例' })).toBeInTheDocument()
    fireEvent.keyDown(native, { key: 'Escape' })
    expect(screen.queryByRole('listbox', { name: '示例' })).not.toBeInTheDocument()
  })

  it('closes when clicking outside and does not open while disabled', async () => {
    const user = userEvent.setup()
    const first = render(<ControlledSelect />)
    await user.click(first.container.querySelector('.select-trigger') as HTMLElement)
    expect(screen.getByRole('listbox', { name: '示例' })).toBeInTheDocument()
    await user.click(document.body)
    expect(screen.queryByRole('listbox', { name: '示例' })).not.toBeInTheDocument()
    first.unmount()

    const second = render(<ControlledSelect disabled />)
    await user.click(second.container.querySelector('.select-trigger') as HTMLElement)
    expect(screen.queryByRole('listbox', { name: '示例' })).not.toBeInTheDocument()
  })
})
