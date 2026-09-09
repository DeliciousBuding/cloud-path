import { act, fireEvent, screen } from '@testing-library/react'
import { describe, expect, it, vi } from 'vitest'
import { PluginUIBridge } from '@/components/plugin-ui/PluginUIBridge'
import { api } from '@/lib/api'
import { renderWithProviders } from '@/test/render'
import type { PluginInstanceView, PluginUISection } from '@/lib/types'

const instance: PluginInstanceView = {
  id: 'server/app-a', tenant_id: 1, edge_id: 'server',
  desired: { instance_id: 'app-a', plugin_id: 'example.app', version: 'v1.0.0', enabled: true, isolation: 'shared', revision: 1, updated_at: 1 },
  has_observed: true, observed: { state: 'running', health: 'HEALTHY', restart_count: 0 },
  edge_online: false, desired_revision: 1, applied_revision: 1, drift: false, stale: false,
}

const customSection: PluginUISection = {
  type: 'custom',
  entry: 'ui/index.html',
  scopes: ['records.read', 'jobs.run', 'config.write'],
}

type SentMessage = { type?: string; nonce?: string; [key: string]: unknown }

function renderBridge(readOnly = false) {
  const rendered = renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
    section={customSection} readOnly={readOnly} />)
  const frame = screen.getByTitle('插件自定义页面') as HTMLIFrameElement
  const sent: SentMessage[] = []
  const postMessage = vi.spyOn(frame.contentWindow!, 'postMessage').mockImplementation((message: unknown) => {
    sent.push(message as SentMessage)
  })
  return { ...rendered, frame, sent, postMessage }
}

function sendFrom(frame: HTMLIFrameElement, data: unknown, source: MessageEventSource | null = frame.contentWindow) {
  act(() => {
    window.dispatchEvent(new MessageEvent('message', { data, origin: 'null', source }))
  })
}

function helloNonce(sent: SentMessage[], index = 0): string {
  const hello = sent.filter((message) => message.type === 'cloudpath:hello')[index]
  expect(hello).toBeDefined()
  expect(typeof hello?.nonce).toBe('string')
  return hello!.nonce!
}

function messageTypes(sent: SentMessage[]): string[] {
  return sent.map((message) => String(message.type))
}

describe('PluginUIBridge', () => {
  it('uses a versioned same-origin asset URL and never grants same-origin sandbox access', () => {
    renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', entry: 'ui/index.html', scopes: ['records.read'] }} />)
    const frame = screen.getByTitle('插件自定义页面')
    expect(frame).toHaveAttribute('src', '/api/plugin-ui/assets/example.app/v2.0.0/ui/index.html')
    expect(frame).toHaveAttribute('sandbox', 'allow-scripts')
    expect(frame.getAttribute('sandbox')).not.toContain('allow-same-origin')
    expect(frame).toHaveAttribute('referrerpolicy', 'no-referrer')
  })

  it('falls back to the desired instance version when catalog version is absent', () => {
    renderWithProviders(<PluginUIBridge pluginId="example.app" instance={instance}
      section={{ type: 'custom', entry: 'ui/index.html', scopes: [] }} />)
    expect(screen.getByTitle('插件自定义页面')).toHaveAttribute('src', '/api/plugin-ui/assets/example.app/v1.0.0/ui/index.html')
  })

  it('fails closed when the custom entry is missing or unsafe', () => {
    const { unmount } = renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', scopes: ['records.read'] }} />)
    expect(screen.getByRole('alert')).toHaveTextContent('自定义页面不可用')
    unmount()
    renderWithProviders(<PluginUIBridge pluginId="example.app" version="v2.0.0" instance={instance}
      section={{ type: 'custom', entry: '../evil.js', scopes: ['records.read'] }} />)
    expect(screen.getByRole('alert')).toHaveTextContent('自定义页面不可用')
    expect(screen.queryByTitle('插件自定义页面')).not.toBeInTheDocument()
  })

  it('sends only a nonce bootstrap on load and waits for a matching ready message before init', () => {
    const { frame, sent } = renderBridge()
    fireEvent.load(frame)

    expect(messageTypes(sent)).toEqual(['cloudpath:hello'])
    expect(sent[0]).not.toHaveProperty('scopes')
    const nonce = helloNonce(sent)

    sendFrom(frame, { type: 'cloudpath:ready', nonce: 'wrong-nonce' })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello'])

    sendFrom(frame, { type: 'cloudpath:request', nonce, id: 'before-ready', method: 'records.list' })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello'])

    sendFrom(frame, { type: 'cloudpath:ready', nonce })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init'])
    expect(sent[1]).toMatchObject({ type: 'cloudpath:init', nonce, scopes: customSection.scopes })
  })

  it('ignores messages from an unexpected source and never replies to a bad nonce', () => {
    const { frame, sent } = renderBridge()
    fireEvent.load(frame)
    const nonce = helloNonce(sent)

    sendFrom(frame, { type: 'cloudpath:ready', nonce }, window)
    sendFrom(frame, { type: 'cloudpath:request', nonce: 'wrong-nonce', id: 'bad', method: 'records.list' }, window)
    expect(messageTypes(sent)).toEqual(['cloudpath:hello'])

    sendFrom(frame, { type: 'cloudpath:ready', nonce })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init'])

    sendFrom(frame, { type: 'cloudpath:request', nonce: 'wrong-nonce', id: 'bad', method: 'records.list' })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init'])
  })

  it('rotates the nonce and resets ready on iframe reload', () => {
    const { frame, sent } = renderBridge()
    fireEvent.load(frame)
    const firstNonce = helloNonce(sent)

    sendFrom(frame, { type: 'cloudpath:ready', nonce: firstNonce })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init'])

    fireEvent.load(frame)
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init', 'cloudpath:hello'])
    const secondNonce = helloNonce(sent, 1)
    expect(secondNonce).not.toBe(firstNonce)

    sendFrom(frame, { type: 'cloudpath:request', nonce: firstNonce, id: 'stale', method: 'records.list' })
    sendFrom(frame, { type: 'cloudpath:ready', nonce: firstNonce })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init', 'cloudpath:hello'])

    sendFrom(frame, { type: 'cloudpath:ready', nonce: secondNonce })
    expect(messageTypes(sent)).toEqual(['cloudpath:hello', 'cloudpath:init', 'cloudpath:hello', 'cloudpath:init'])
    expect(sent[3]).toMatchObject({ type: 'cloudpath:init', nonce: secondNonce })
  })

  it('rejects write methods for a read-only viewer without calling the API', () => {
    const runAppJob = vi.spyOn(api, 'runAppJob')
    const updatePluginInstance = vi.spyOn(api, 'updatePluginInstance')
    const { frame, sent } = renderBridge(true)
    fireEvent.load(frame)
    const nonce = helloNonce(sent)
    sendFrom(frame, { type: 'cloudpath:ready', nonce })

    sendFrom(frame, { type: 'cloudpath:request', nonce, id: 'run', method: 'jobs.run', params: { job_id: 'dispense' } })
    sendFrom(frame, { type: 'cloudpath:request', nonce, id: 'update', method: 'config.update', params: { config: { timezone: 'Asia/Shanghai' } } })

    const responses = sent.filter((message) => message.type === 'cloudpath:response')
    expect(responses).toHaveLength(2)
    expect(responses[0]).toMatchObject({ nonce, id: 'run', ok: false, error: { code: 'forbidden_readonly' } })
    expect(responses[1]).toMatchObject({ nonce, id: 'update', ok: false, error: { code: 'forbidden_readonly' } })
    expect(runAppJob).not.toHaveBeenCalled()
    expect(updatePluginInstance).not.toHaveBeenCalled()
  })
})
