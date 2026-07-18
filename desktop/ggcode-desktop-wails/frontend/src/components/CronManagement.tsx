import React, { useState, useEffect, useCallback } from 'react'
import { ChevronLeft, Clock, Plus, Trash2, Pause, Play, Pencil, Sparkles, RefreshCw, ChevronDown, ChevronRight } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'

interface CronJobInfo {
  id: string
  cronExpr: string
  prompt: string
  recurring: boolean
  queueIfBusy: boolean
  paused: boolean
  createdAt: string
  nextFire: string
}

interface Props {
  onBack: () => void
}

export function CronManagement({ onBack }: Props) {
  const { t } = useTranslation()
  const [jobs, setJobs] = useState<CronJobInfo[]>([])
  const [loading, setLoading] = useState(true)
  const [editing, setEditing] = useState<CronJobInfo | null>(null)
  const [isCreating, setIsCreating] = useState(false)
  const [expandedId, setExpandedId] = useState<string | null>(null)
  const [message, setMessage] = useState('')

  const loadJobs = useCallback(async () => {
    setLoading(true)
    try {
      const result = await App.ListCronJobs()
      setJobs((result as CronJobInfo[]) || [])
    } catch (e) {
      setMessage(t('cron.failedLoad', { 0: String(e) }))
    } finally {
      setLoading(false)
    }
  }, [t])

  useEffect(() => {
    loadJobs()
  }, [loadJobs])

  const handleDelete = async (id: string) => {
    try {
      await App.DeleteCronJob(id)
      setJobs(jobs.filter(j => j.id !== id))
      setMessage(t('cron.jobDeleted'))
    } catch (e) {
      setMessage(t('cron.deleteFailed', { 0: String(e) }))
    }
  }

  const handlePauseResume = async (job: CronJobInfo) => {
    try {
      if (job.paused) {
        await App.ResumeCronJob(job.id)
      } else {
        await App.PauseCronJob(job.id)
      }
      await loadJobs()
    } catch (e) {
      setMessage(t('cron.failed', { 0: String(e) }))
    }
  }

  const handleSave = async (data: { id?: string; cronExpr: string; prompt: string; recurring: boolean; queueIfBusy: boolean }) => {
    try {
      if (data.id) {
        await App.UpdateCronJob(data.id, data.cronExpr, data.prompt, data.queueIfBusy)
        setMessage(t('cron.jobUpdated'))
      } else {
        await App.CreateCronJob(data.cronExpr, data.prompt, data.recurring, data.queueIfBusy)
        setMessage(t('cron.jobCreated'))
      }
      setEditing(null)
      setIsCreating(false)
      await loadJobs()
    } catch (e) {
      setMessage(t('cron.saveFailed', { 0: String(e) }))
    }
  }

  if (isCreating || editing) {
    return (
      <CronJobEditor
        job={editing}
        onBack={() => { setEditing(null); setIsCreating(false) }}
        onSave={handleSave}
      />
    )
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--color-border, #30363d)', flexShrink: 0 }}>
        <button onClick={onBack} className="nav-rail-btn" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-secondary)', display: 'flex', padding: 4, borderRadius: 6 }}>
          <ChevronLeft size={20} />
        </button>
        <Clock size={20} style={{ color: 'var(--color-primary, #58A6FF)' }} />
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>{t('cron.title')}</h2>
        <div style={{ flex: 1 }} />
        <button onClick={loadJobs} title={t('cron.refresh')} className="nav-rail-btn" style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-secondary)', display: 'flex', padding: 4, borderRadius: 6 }}>
          <RefreshCw size={16} />
        </button>
        <button onClick={() => setIsCreating(true)} style={{ display: 'flex', alignItems: 'center', gap: 6, padding: '6px 12px', borderRadius: 6, background: 'var(--color-primary, #58A6FF)', color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500 }}>
          <Plus size={16} /> {t('cron.newTask')}
        </button>
      </div>

      {/* Message */}
      {message && (
        <div style={{ padding: '8px 16px', fontSize: 13, color: 'var(--text-secondary)', background: 'var(--color-nav-hover, rgba(255,255,255,0.04))' }}>
          {message}
        </div>
      )}

      {/* Job list */}
      <div style={{ flex: 1, overflow: 'auto', padding: '8px 16px' }}>
        {loading ? (
          <div style={{ color: 'var(--text-secondary)', textAlign: 'center', padding: 40 }}>{t('cron.loading')}</div>
        ) : jobs.length === 0 ? (
          <div style={{ color: 'var(--text-secondary)', textAlign: 'center', padding: 40 }}>
            <Clock size={48} style={{ opacity: 0.3, marginBottom: 12 }} />
            <p>{t('cron.empty')}</p>
          </div>
        ) : (
          <div style={{ display: 'flex', flexDirection: 'column', gap: 8 }}>
            {jobs.map(job => (
              <JobCard
                key={job.id}
                job={job}
                expanded={expandedId === job.id}
                onToggleExpand={() => setExpandedId(expandedId === job.id ? null : job.id)}
                onEdit={() => setEditing(job)}
                onDelete={() => handleDelete(job.id)}
                onPauseResume={() => handlePauseResume(job)}
              />
            ))}
          </div>
        )}
      </div>
    </div>
  )
}

function JobCard({ job, expanded, onToggleExpand, onEdit, onDelete, onPauseResume }: {
  job: CronJobInfo
  expanded: boolean
  onToggleExpand: () => void
  onEdit: () => void
  onDelete: () => void
  onPauseResume: () => void
}) {
  const { t } = useTranslation()
  const statusColor = job.paused ? '#f59e0b' : '#3FB950'
  const statusText = job.paused ? t('cron.paused') : t('cron.active')

  return (
    <div style={{ border: '1px solid var(--color-border, #30363d)', borderRadius: 8, overflow: 'hidden' }}>
      {/* Summary row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 8, padding: '10px 12px', cursor: 'pointer' }} onClick={onToggleExpand}>
        {expanded ? <ChevronDown size={16} style={{ color: 'var(--text-secondary)' }} /> : <ChevronRight size={16} style={{ color: 'var(--text-secondary)' }} />}
        <code style={{ fontSize: 13, color: '#58A6FF', fontWeight: 600, minWidth: 100 }}>{job.cronExpr}</code>
        <span style={{ fontSize: 13, color: 'var(--text-primary)', flex: 1, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
          {job.prompt.slice(0, 80) || '(empty)'}
        </span>
        <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: statusColor + '22', color: statusColor, fontWeight: 600 }}>
          {statusText}
        </span>
        {!job.recurring && (
          <span style={{ fontSize: 11, padding: '2px 8px', borderRadius: 4, background: '#6366f122', color: '#818cf8', fontWeight: 600 }}>
            {t('cron.oneShot')}
          </span>
        )}
      </div>

      {/* Expanded detail */}
      {expanded && (
        <div style={{ padding: '0 12px 12px 44px', borderTop: '1px solid var(--color-border, #30363d)' }}>
          <div style={{ marginTop: 8 }}>
            <label style={{ fontSize: 11, color: 'var(--text-secondary)', textTransform: 'uppercase', fontWeight: 600 }}>{t('cron.prompt')}</label>
            <pre style={{ margin: '4px 0 12px', padding: 8, background: 'var(--color-nav-hover, rgba(255,255,255,0.04))', borderRadius: 6, fontSize: 13, whiteSpace: 'pre-wrap', wordBreak: 'break-word', color: 'var(--text-primary)' }}>
              {job.prompt}
            </pre>
          </div>
          <div style={{ display: 'flex', gap: 16, marginBottom: 12, fontSize: 12, color: 'var(--text-secondary)', flexWrap: 'wrap' }}>
            <span>{t('cron.id')}: <code>{job.id}</code></span>
            {job.createdAt && <span>{t('cron.createdAt')}: {new Date(job.createdAt).toLocaleString()}</span>}
            {job.nextFire && <span>{t('cron.nextFire')}: {new Date(job.nextFire).toLocaleString()}</span>}
            <span>{t('cron.queueLabel')}: {job.queueIfBusy ? t('cron.yes') : t('cron.no')}</span>
          </div>
          {/* Actions */}
          <div style={{ display: 'flex', gap: 8 }}>
            <button onClick={onEdit} style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: 6, background: 'transparent', border: '1px solid var(--color-border, #30363d)', cursor: 'pointer', color: 'var(--text-primary)', fontSize: 12 }}>
              <Pencil size={14} /> {t('cron.edit')}
            </button>
            <button onClick={onPauseResume} style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: 6, background: 'transparent', border: '1px solid var(--color-border, #30363d)', cursor: 'pointer', color: 'var(--text-primary)', fontSize: 12 }}>
              {job.paused ? <Play size={14} /> : <Pause size={14} />} {job.paused ? t('cron.resume') : t('cron.pause')}
            </button>
            <button onClick={onDelete} style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: 6, background: 'transparent', border: '1px solid #ef444455', cursor: 'pointer', color: '#ef4444', fontSize: 12 }}>
              <Trash2 size={14} /> {t('cron.delete')}
            </button>
          </div>
        </div>
      )}
    </div>
  )
}

function CronJobEditor({ job, onBack, onSave }: {
  job: CronJobInfo | null
  onBack: () => void
  onSave: (data: { id?: string; cronExpr: string; prompt: string; recurring: boolean; queueIfBusy: boolean }) => void
}) {
  const { t } = useTranslation()
  const [cronExpr, setCronExpr] = useState(job?.cronExpr || '')
  const [prompt, setPrompt] = useState(job?.prompt || '')
  const [recurring, setRecurring] = useState(job?.recurring ?? true)
  const [queueIfBusy, setQueueIfBusy] = useState(job?.queueIfBusy ?? false)
  const [generating, setGenerating] = useState(false)
  const [aiDescription, setAiDescription] = useState('')
  const [showAiInput, setShowAiInput] = useState(false)
  const [error, setError] = useState('')

  const handleGenerate = async () => {
    if (!aiDescription.trim()) {
      setError(t('cron.descRequired'))
      return
    }
    setGenerating(true)
    setError('')
    try {
      const result = await App.GenerateCronPrompt(aiDescription)
      setPrompt(result)
      setShowAiInput(false)
      setAiDescription('')
    } catch (e) {
      setError(t('cron.aiGenFailed', { 0: String(e) }))
    } finally {
      setGenerating(false)
    }
  }

  const handleSubmit = () => {
    if (!cronExpr.trim()) {
      setError(t('cron.cronExprRequired'))
      return
    }
    if (!prompt.trim()) {
      setError(t('cron.promptRequired'))
      return
    }
    onSave({
      id: job?.id,
      cronExpr: cronExpr.trim(),
      prompt: prompt.trim(),
      recurring,
      queueIfBusy,
    })
  }

  return (
    <div style={{ display: 'flex', flexDirection: 'column', height: '100%', overflow: 'hidden' }}>
      {/* Header */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 12, padding: '12px 16px', borderBottom: '1px solid var(--color-border, #30363d)', flexShrink: 0 }}>
        <button onClick={onBack} style={{ background: 'transparent', border: 'none', cursor: 'pointer', color: 'var(--text-secondary)', display: 'flex', padding: 4, borderRadius: 6 }}>
          <ChevronLeft size={20} />
        </button>
        <h2 style={{ margin: 0, fontSize: 16, fontWeight: 600 }}>{job ? t('cron.editTask') : t('cron.newTaskTitle')}</h2>
      </div>

      {/* Form */}
      <div style={{ flex: 1, overflow: 'auto', padding: '16px 24px', display: 'flex', flexDirection: 'column', gap: 16 }}>
        {/* Cron expression */}
        <div>
          <label style={{ display: 'block', fontSize: 13, fontWeight: 600, marginBottom: 4 }}>{t('cron.cronExpr')}</label>
          <input
            type="text"
            value={cronExpr}
            onChange={e => setCronExpr(e.target.value)}
            placeholder="*/5 * * * *"
            style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid var(--color-border, #30363d)', background: 'var(--color-bg-input, #0d1117)', color: 'var(--text-primary)', fontSize: 14, fontFamily: 'monospace', boxSizing: 'border-box' }}
          />
          <p style={{ fontSize: 11, color: 'var(--text-secondary)', marginTop: 4 }}>
            {t('cron.cronHint')}
          </p>
        </div>

        {/* Prompt */}
        <div>
          <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4 }}>
            <label style={{ fontSize: 13, fontWeight: 600 }}>{t('cron.prompt')}</label>
            <button
              onClick={() => setShowAiInput(!showAiInput)}
              style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '4px 10px', borderRadius: 6, background: showAiInput ? 'var(--color-primary, #58A6FF)22' : 'transparent', border: '1px solid var(--color-border, #30363d)', cursor: 'pointer', color: 'var(--color-primary, #58A6FF)', fontSize: 12 }}
            >
              <Sparkles size={14} /> {t('cron.generateWithAI')}
            </button>
          </div>

          {/* AI input */}
          {showAiInput && (
            <div style={{ marginBottom: 8, padding: 12, border: '1px solid var(--color-border, #30363d)', borderRadius: 8, background: 'var(--color-nav-hover, rgba(255,255,255,0.04))' }}>
              <p style={{ fontSize: 12, color: 'var(--text-secondary)', marginTop: 0, marginBottom: 8 }}>
                {t('cron.aiDescription')}
              </p>
              <input
                type="text"
                value={aiDescription}
                onChange={e => setAiDescription(e.target.value)}
                placeholder={t('cron.aiPlaceholder')}
                style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid var(--color-border, #30363d)', background: 'var(--color-bg-input, #0d1117)', color: 'var(--text-primary)', fontSize: 13, boxSizing: 'border-box' }}
                onKeyDown={e => { if (e.key === 'Enter') handleGenerate() }}
              />
              <div style={{ display: 'flex', gap: 8, marginTop: 8 }}>
                <button
                  onClick={handleGenerate}
                  disabled={generating}
                  style={{ display: 'flex', alignItems: 'center', gap: 4, padding: '6px 14px', borderRadius: 6, background: 'var(--color-primary, #58A6FF)', color: '#fff', border: 'none', cursor: generating ? 'wait' : 'pointer', fontSize: 13, opacity: generating ? 0.6 : 1 }}
                >
                  {generating ? <RefreshCw size={14} className="spin" /> : <Sparkles size={14} />} {generating ? t('cron.generating') : t('cron.generate')}
                </button>
                <button onClick={() => setShowAiInput(false)} style={{ padding: '6px 14px', borderRadius: 6, background: 'transparent', border: '1px solid var(--color-border, #30363d)', cursor: 'pointer', color: 'var(--text-secondary)', fontSize: 13 }}>
                  {t('cron.cancel')}
                </button>
              </div>
            </div>
          )}

          <textarea
            value={prompt}
            onChange={e => setPrompt(e.target.value)}
            placeholder={t('cron.promptPlaceholder')}
            rows={8}
            style={{ width: '100%', padding: '8px 12px', borderRadius: 6, border: '1px solid var(--color-border, #30363d)', background: 'var(--color-bg-input, #0d1117)', color: 'var(--text-primary)', fontSize: 13, fontFamily: 'monospace', resize: 'vertical', minHeight: 120, boxSizing: 'border-box' }}
          />
        </div>

        {/* Options */}
        <div style={{ display: 'flex', gap: 24 }}>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13 }}>
            <input type="checkbox" checked={recurring} onChange={e => setRecurring(e.target.checked)} />
            {t('cron.recurring')}
          </label>
          <label style={{ display: 'flex', alignItems: 'center', gap: 8, cursor: 'pointer', fontSize: 13 }}>
            <input type="checkbox" checked={queueIfBusy} onChange={e => setQueueIfBusy(e.target.checked)} />
            {t('cron.queueIfBusy')}
          </label>
        </div>

        {/* Error */}
        {error && (
          <div style={{ padding: '8px 12px', borderRadius: 6, background: '#ef444422', color: '#ef4444', fontSize: 13 }}>
            {error}
          </div>
        )}
      </div>

      {/* Footer */}
      <div style={{ display: 'flex', justifyContent: 'flex-end', gap: 8, padding: '12px 24px', borderTop: '1px solid var(--color-border, #30363d)' }}>
        <button onClick={onBack} style={{ padding: '8px 16px', borderRadius: 6, background: 'transparent', border: '1px solid var(--color-border, #30363d)', cursor: 'pointer', color: 'var(--text-primary)', fontSize: 13 }}>
          {t('cron.cancel')}
        </button>
        <button onClick={handleSubmit} style={{ padding: '8px 16px', borderRadius: 6, background: 'var(--color-primary, #58A6FF)', color: '#fff', border: 'none', cursor: 'pointer', fontSize: 13, fontWeight: 500 }}>
          {job ? t('cron.update') : t('cron.create')}
        </button>
      </div>
    </div>
  )
}
