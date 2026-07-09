import React, { useMemo } from 'react'
import { CheckCircle2, Circle, Columns3, Users, X } from 'lucide-react'
import { useTranslation, type TranslationKey } from '../i18n'

export interface TeamBoardTask {
  id: string
  subject: string
  description?: string
  activeForm?: string
  status: 'pending' | 'in_progress' | 'completed' | string
  owner?: string
  assignee?: string
  blocks?: string[]
  blockedBy?: string[]
  metadata?: Record<string, string>
  createdAt?: string
  updatedAt?: string
}

export interface TeamBoardTeammate {
  id: string
  name: string
  color?: string
  status: 'idle' | 'working' | 'shutting_down' | string
  currentTask?: string
  lastResult?: string
}

export interface TeamBoardSnapshot {
  id: string
  name: string
  leaderID: string
  teammates: TeamBoardTeammate[]
  tasks: TeamBoardTask[]
  createdAt?: string
}

interface TeamBoardProps {
  teams: TeamBoardSnapshot[]
  onClose: () => void
  onSelectTeammate?: (teammateID: string) => void
}

const columns: Array<{ key: string; titleKey: TranslationKey; emptyKey: TranslationKey }> = [
  { key: 'pending', titleKey: 'team.pending', emptyKey: 'team.noPending' },
  { key: 'in_progress', titleKey: 'team.inProgress', emptyKey: 'team.noInProgress' },
  { key: 'completed', titleKey: 'team.done', emptyKey: 'team.noDone' },
]

export function TeamBoard({ teams, onClose, onSelectTeammate }: TeamBoardProps) {
  const { t } = useTranslation()
  const totalTeammates = useMemo(() => teams.reduce((sum, t) => sum + (t.teammates?.length || 0), 0), [teams])
  const totalTasks = useMemo(() => teams.reduce((sum, t) => sum + (t.tasks?.length || 0), 0), [teams])

  return (
    <aside style={{
      width: 360,
      minWidth: 320,
      maxWidth: 420,
      height: '100%',
      borderLeft: '1px solid var(--color-border)',
      background: 'var(--color-surface)',
      display: 'flex',
      flexDirection: 'column',
      flexShrink: 0,
    }}>
      <div style={{
        padding: '12px 14px',
        borderBottom: '1px solid var(--color-border)',
        display: 'flex',
        alignItems: 'center',
        justifyContent: 'space-between',
        gap: 8,
      }}>
        <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
          <Columns3 size={16} style={{ color: 'var(--color-primary)' }} />
          <div>
            <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--text-primary)' }}>Team Board</div>
            <div style={{ fontSize: 11, color: 'var(--text-tertiary)' }}>
              {teams.length} {t('team.teams')} · {totalTeammates} {t('team.teammates')} · {totalTasks} {t('team.tasks')}
            </div>
          </div>
        </div>
        <button type="button" onClick={onClose} title="Close team board" aria-label="Close team board" style={{
          width: 28,
          height: 28,
          borderRadius: 'var(--radius-md)',
          border: '1px solid var(--color-border)',
          background: 'var(--color-card)',
          color: 'var(--text-secondary)',
          display: 'flex',
          alignItems: 'center',
          justifyContent: 'center',
          cursor: 'pointer',
        }}>
          <X size={14} />
        </button>
      </div>

      <div style={{ flex: 1, overflowY: 'auto', padding: 12, display: 'flex', flexDirection: 'column', gap: 12 }}>
        {teams.length === 0 ? (
          <div style={{
            padding: 16,
            borderRadius: 'var(--radius-lg)',
            border: '1px dashed var(--color-border)',
            color: 'var(--text-tertiary)',
            fontSize: 12,
            textAlign: 'center',
          }}>
            {t('team.startPrompt')}
          </div>
        ) : teams.map(team => (
          <TeamSection key={team.id} team={team} onSelectTeammate={onSelectTeammate} />
        ))}
      </div>
    </aside>
  )
}

function TeamSection({ team, onSelectTeammate }: { team: TeamBoardSnapshot; onSelectTeammate?: (teammateID: string) => void }) {
  const { t } = useTranslation()
  const teammateByID = useMemo(() => {
    const map = new Map<string, TeamBoardTeammate>()
    for (const tm of team.teammates || []) map.set(tm.id, tm)
    return map
  }, [team.teammates])

  return (
    <section style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
      <div style={{
        padding: '10px 12px',
        borderRadius: 'var(--radius-lg)',
        background: 'var(--color-card)',
        border: '1px solid var(--color-border)',
      }}>
        <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
          <div style={{ fontSize: 13, fontWeight: 700, color: 'var(--text-primary)' }}>{team.name || team.id}</div>
          <div style={{ fontSize: 10, color: 'var(--text-tertiary)', fontFamily: 'var(--font-mono)' }}>{team.id}</div>
        </div>
        <div style={{ marginTop: 8, display: 'flex', flexDirection: 'column', gap: 6 }}>
          {(team.teammates || []).length === 0 ? (
            <div style={{ fontSize: 12, color: 'var(--text-tertiary)' }}>{t('team.noTeammates')}</div>
          ) : team.teammates.map(tm => (
            <button key={tm.id} type="button" onClick={() => onSelectTeammate?.(tm.id)} aria-label={`Teammate ${tm.name}: ${tm.status}${tm.currentTask ? ', task: ' + tm.currentTask : ''}`} title={`Teammate ${tm.name}: ${tm.status}`} style={{
              textAlign: 'left',
              border: '1px solid var(--color-border)',
              borderRadius: 'var(--radius-md)',
              background: 'rgba(255,255,255,0.02)',
              padding: '7px 8px',
              color: 'var(--text-secondary)',
              cursor: onSelectTeammate ? 'pointer' : 'default',
            }}>
              <div style={{ display: 'flex', alignItems: 'center', justifyContent: 'space-between', gap: 8 }}>
                <span style={{ display: 'flex', alignItems: 'center', gap: 6, minWidth: 0 }}>
                  <Users size={12} style={{ color: statusColor(tm.status) }} />
                  <span style={{ fontSize: 12, fontWeight: 600, color: 'var(--text-primary)', overflow: 'hidden', textOverflow: 'ellipsis' }}>{tm.name}</span>
                </span>
                <StatusPill status={tm.status} />
              </div>
              {tm.currentTask && <div style={{ marginTop: 5, fontSize: 11, color: 'var(--text-tertiary)', overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>{tm.currentTask}</div>}
            </button>
          ))}
        </div>
      </div>

      <div style={{ display: 'flex', flexDirection: 'column', gap: 10 }}>
        {columns.map(col => {
          const colTasks = (team.tasks || []).filter(task => (task.status || 'pending') === col.key)
          return (
            <div key={col.key} style={{ display: 'flex', flexDirection: 'column', gap: 6 }}>
              <div style={{
                display: 'flex',
                alignItems: 'center',
                justifyContent: 'space-between',
                fontSize: 11,
                fontWeight: 700,
                textTransform: 'uppercase',
                letterSpacing: 0.4,
                color: 'var(--text-tertiary)',
              }}>
                <span>{t(col.titleKey)}</span>
                <span>{colTasks.length}</span>
              </div>
              {colTasks.length === 0 ? (
                <div style={{
                  padding: '8px 10px',
                  borderRadius: 'var(--radius-md)',
                  border: '1px dashed var(--color-border)',
                  color: 'var(--text-tertiary)',
                  fontSize: 11,
                }}>{t(col.emptyKey)}</div>
              ) : colTasks.map(task => (
                <TaskCard key={task.id} task={task} teammate={teammateByID.get(task.owner || task.assignee || '')} />
              ))}
            </div>
          )
        })}
      </div>
    </section>
  )
}

function TaskCard({ task, teammate }: { task: TeamBoardTask; teammate?: TeamBoardTeammate }) {
  const blocked = (task.blockedBy || []).length > 0
  return (
    <div style={{
      padding: '9px 10px',
      borderRadius: 'var(--radius-md)',
      background: 'var(--color-card)',
      border: `1px solid ${blocked ? 'var(--color-warning)' : 'var(--color-border)'}`,
      boxShadow: '0 1px 2px rgba(0,0,0,0.08)',
    }}>
      <div style={{ display: 'flex', alignItems: 'flex-start', gap: 8 }}>
        {task.status === 'completed' ? <CheckCircle2 size={14} style={{ color: 'var(--color-success)', marginTop: 1 }} /> : <Circle size={14} style={{ color: statusColor(task.status), marginTop: 1 }} />}
        <div style={{ minWidth: 0, flex: 1 }}>
          <div style={{ fontSize: 12, fontWeight: 650, color: 'var(--text-primary)', lineHeight: 1.35 }}>{task.subject || task.id}</div>
          {task.activeForm && <div style={{ marginTop: 4, fontSize: 11, color: 'var(--color-primary)' }}>{task.activeForm}</div>}
          {task.description && <div style={{ marginTop: 5, fontSize: 11, color: 'var(--text-tertiary)', lineHeight: 1.35, display: '-webkit-box', WebkitLineClamp: 2, WebkitBoxOrient: 'vertical', overflow: 'hidden' }}>{task.description}</div>}
          <div style={{ marginTop: 7, display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
            <span style={{ fontSize: 10, fontFamily: 'var(--font-mono)', color: 'var(--text-tertiary)' }}>{task.id}</span>
            {(task.owner || task.assignee || teammate) && <span style={{ fontSize: 10, color: 'var(--text-secondary)' }}>· {teammate?.name || task.owner || task.assignee}</span>}
            {blocked && <span style={{ fontSize: 10, color: 'var(--color-warning)' }}>blocked by {(task.blockedBy || []).length}</span>}
          </div>
        </div>
      </div>
    </div>
  )
}

function StatusPill({ status }: { status: string }) {
  return (
    <span style={{
      fontSize: 10,
      padding: '2px 6px',
      borderRadius: 999,
      border: '1px solid var(--color-border)',
      color: statusColor(status),
      background: 'rgba(255,255,255,0.03)',
      whiteSpace: 'nowrap',
    }}>{formatStatus(status)}</span>
  )
}

function statusColor(status: string): string {
  switch (status) {
    case 'working':
    case 'in_progress':
      return 'var(--color-warning)'
    case 'completed':
    case 'idle':
      return 'var(--color-success)'
    case 'shutting_down':
      return 'var(--color-error)'
    default:
      return 'var(--text-tertiary)'
  }
}

function formatStatus(status: string): string {
  return status.replace(/_/g, ' ')
}
