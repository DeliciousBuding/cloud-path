import { useEffect, useMemo, useState } from 'react'
import { useQuery } from '@tanstack/react-query'
import { useTranslation } from 'react-i18next'
import { Button, Checkbox } from '@/components/ui'
import { api } from '@/lib/api'
import type { AppBindingCandidateView, AppBindingRequirementView, AppBindingView } from '@/lib/types'

interface Target {
  requirement_id: string
  entity_id: string
  device_id: string
}

function targetKey(target: Pick<Target, 'device_id' | 'entity_id'>): string {
  return `${target.device_id}\u0000${target.entity_id}`
}

function parseTargets(raw: string | undefined): Target[] {
  if (!raw) return []
  try {
    const parsed = JSON.parse(raw) as unknown
    if (!Array.isArray(parsed)) return []
    return parsed.flatMap((item): Target[] => {
      if (!item || typeof item !== 'object') return []
      const row = item as Record<string, unknown>
      if (typeof row.requirement_id !== 'string' || typeof row.entity_id !== 'string') return []
      return [{
        requirement_id: row.requirement_id,
        entity_id: row.entity_id,
        device_id: typeof row.device_id === 'string' ? row.device_id : '',
      }]
    })
  } catch {
    return []
  }
}

function runtimeTargets(bindings: AppBindingView[]): Target[] {
  return bindings.map((binding) => ({
    requirement_id: binding.requirement_id,
    entity_id: binding.entity_id,
    device_id: binding.device_id ?? '',
  }))
}

function candidateLabel(candidate: AppBindingCandidateView): string {
  return candidate.name ? `${candidate.name} · ${candidate.device_id}` : `${candidate.entity_id} · ${candidate.device_id}`
}

export function DeviceBindingSelector({ instanceId, value, onChange, disabled = false }: {
  instanceId: string
  value?: string
  onChange: (value: string | undefined) => void
  disabled?: boolean
}) {
  const { t } = useTranslation('plugin')
  const query = useQuery({
    queryKey: ['plugin-instance-bindings', instanceId],
    queryFn: ({ signal }) => api.appBindings(instanceId, signal),
    enabled: Boolean(instanceId),
  })
  const explicit = value !== undefined
  const [targets, setTargets] = useState<Target[]>(() => parseTargets(value))

  useEffect(() => {
    if (!query.data) return
    setTargets(explicit ? parseTargets(value) : runtimeTargets(query.data.bindings))
  }, [explicit, query.data, value])

  const byRequirement = useMemo(() => {
    const out = new Map<string, Target[]>()
    for (const target of targets) {
      const list = out.get(target.requirement_id) ?? []
      list.push(target)
      out.set(target.requirement_id, list)
    }
    return out
  }, [targets])

  const updateTargets = (next: Target[]) => {
    const ordered = next.slice().sort((a, b) => {
      const reqOrder = (query.data?.requirements ?? []).findIndex((r) => r.id === a.requirement_id)
        - (query.data?.requirements ?? []).findIndex((r) => r.id === b.requirement_id)
      if (reqOrder !== 0) return reqOrder
      return targetKey(a).localeCompare(targetKey(b))
    })
    setTargets(ordered)
    onChange(ordered.length ? JSON.stringify(ordered) : undefined)
  }

  const toggle = (requirement: AppBindingRequirementView, candidate: AppBindingCandidateView, checked: boolean) => {
    const target: Target = { requirement_id: requirement.id, entity_id: candidate.entity_id, device_id: candidate.device_id }
    let next = targets.filter((item) => targetKey(item) !== targetKey(target))
    if (requirement.cardinality === 'one' || requirement.cardinality === 'zero-or-one') {
      next = next.filter((item) => item.requirement_id !== requirement.id)
    }
    if (checked) next = [...next, target]
    updateTargets(next)
  }

  if (!instanceId) return null

  return (
    <div className="rounded-tile bg-surface-2 p-3">
      <p className="text-compact font-medium">{t('form.deviceTargets')}</p>
      <p className="mt-1 text-meta leading-relaxed text-ink-3">{t('form.deviceTargetsHint')}</p>
      {query.isLoading ? (
        <p className="mt-3 text-meta text-ink-3">{t('form.deviceTargetsLoading')}</p>
      ) : query.isError ? (
        <p className="mt-3 text-meta text-bad">{t('form.deviceTargetsLoadFailed')}</p>
      ) : (query.data?.requirements ?? []).length === 0 ? (
        <p className="mt-3 text-meta text-ink-3">{t('form.deviceTargetsEmpty')}</p>
      ) : (
        <div className="mt-3 space-y-4">
          {!query.data?.running && <p className="text-meta text-ink-3">{t('form.deviceTargetsStopped')}</p>}
          {(query.data?.requirements ?? []).map((requirement) => {
            const selected = byRequirement.get(requirement.id) ?? []
            const selectedKeys = new Set(selected.map(targetKey))
            const candidates = (query.data?.candidates ?? []).filter((candidate) => candidate.capabilities.includes(requirement.capability))
            return (
              <fieldset key={requirement.id} className="min-w-0">
                <legend className="text-meta font-medium text-ink-2">
                  {requirement.id} <span className="font-mono text-ink-3">{requirement.capability}</span>
                </legend>
                {candidates.length === 0 ? (
                  <p className="mt-1.5 text-meta text-ink-3">{t('form.deviceTargetsEmpty')}</p>
                ) : (
                  <div className="mt-1.5 grid gap-1.5 sm:grid-cols-2">
                    {candidates.map((candidate) => {
                      const key = targetKey(candidate)
                      return (
                        <label key={key} className="flex min-w-0 items-start gap-2 rounded-tile bg-surface px-2.5 py-2 text-meta">
                          <Checkbox
                            checked={selectedKeys.has(key)}
                            disabled={disabled}
                            onChange={(event) => toggle(requirement, candidate, event.target.checked)}
                          />
                          <span className="min-w-0 break-all">
                            <span className="block truncate text-ink">{candidateLabel(candidate)}</span>
                            <span className="block font-mono text-ink-3">{candidate.entity_id}</span>
                          </span>
                        </label>
                      )
                    })}
                  </div>
                )}
              </fieldset>
            )
          })}
          {explicit && (
            <Button type="button" variant="ghost" size="sm" disabled={disabled} onClick={() => onChange(undefined)}>
              {t('form.resetBindings')}
            </Button>
          )}
        </div>
      )}
    </div>
  )
}
