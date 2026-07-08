import React, { useState } from 'react'
import { HelpCircle, X, Send } from 'lucide-react'
import * as App from '../../wailsjs/go/main/App'
import { useTranslation } from '../i18n'

export interface AskUserChoice {
  id: string
  label: string
}

export interface AskUserQuestion {
  id: string
  title: string
  prompt: string
  kind: 'single' | 'multi' | 'text'
  choices: AskUserChoice[]
  allow_freeform: boolean
}

export interface AskUserRequest {
  requestID: string
  title: string
  questions: AskUserQuestion[]
}

interface AnswerState {
  selectedChoiceIds: string[]
  freeformText: string
}

interface AskUserDialogProps {
  request: AskUserRequest
  onClose: () => void
}

export function AskUserDialog({ request, onClose }: AskUserDialogProps) {
  const { t } = useTranslation()
  const [responding, setResponding] = useState(false)

  // Initialize answer state for each question
  const [answers, setAnswers] = useState<Record<string, AnswerState>>(() => {
    const initial: Record<string, AnswerState> = {}
    for (const q of request.questions) {
      initial[q.id] = {
        selectedChoiceIds: q.kind === 'single' && q.choices.length > 0 ? [q.choices[0].id] : [],
        freeformText: '',
      }
    }
    return initial
  })

  const updateAnswer = (questionId: string, update: Partial<AnswerState>) => {
    setAnswers(prev => ({
      ...prev,
      [questionId]: { ...prev[questionId], ...update },
    }))
  }

  const handleSingleSelect = (questionId: string, choiceId: string) => {
    updateAnswer(questionId, { selectedChoiceIds: [choiceId] })
  }

  const handleMultiToggle = (questionId: string, choiceId: string) => {
    const current = answers[questionId].selectedChoiceIds
    const next = current.includes(choiceId)
      ? current.filter(id => id !== choiceId)
      : [...current, choiceId]
    updateAnswer(questionId, { selectedChoiceIds: next })
  }

  const handleSubmit = async () => {
    if (responding) return
    setResponding(true)
    try {
      const answerArray = request.questions.map(q => {
        const a = answers[q.id] || { selectedChoiceIds: [], freeformText: '' }
        return {
          id: q.id,
          selected_choice_ids: a.selectedChoiceIds,
          freeform_text: a.freeformText,
          answered: true,
        }
      })
      const responsePayload = {
        status: 'submitted',
        answers: answerArray,
      }
      await App.RespondAskUser(request.requestID, JSON.stringify(responsePayload))
    } catch (e) {
      console.error('AskUser response error:', e)
    }
    onClose()
  }

  const handleCancel = async () => {
    if (responding) return
    setResponding(true)
    try {
      await App.RespondAskUser(request.requestID, JSON.stringify({
        status: 'cancelled',
        answers: [],
      }))
    } catch (e) {
      console.error('AskUser cancel error:', e)
    }
    onClose()
  }

  // Styles
  const labelStyle: React.CSSProperties = {
    fontSize: 13, fontWeight: 600, color: 'var(--text-primary)', marginBottom: 6,
  }
  const promptStyle: React.CSSProperties = {
    fontSize: 12, color: 'var(--text-tertiary)', marginBottom: 8, lineHeight: 1.4,
  }
  const inputStyle: React.CSSProperties = {
    width: '100%', padding: '8px 10px', borderRadius: 'var(--radius-md)',
    background: 'var(--color-card)', border: '1px solid var(--color-border)',
    color: 'var(--text-primary)', fontSize: 13, outline: 'none',
    fontFamily: 'inherit', boxSizing: 'border-box' as const,
  }
  const radioCheckStyle: React.CSSProperties = {
    width: 16, height: 16, borderRadius: '50%',
    border: '2px solid var(--color-border)',
    display: 'flex', alignItems: 'center', justifyContent: 'center',
    flexShrink: 0, cursor: 'pointer', transition: 'all 0.15s',
  }
  const checkboxStyle: React.CSSProperties = {
    ...radioCheckStyle,
    borderRadius: 3,
  }

  return (
    <div style={{
      position: 'fixed', inset: 0,
      background: 'rgba(0,0,0,0.6)',
      display: 'flex', alignItems: 'center', justifyContent: 'center',
      zIndex: 1000,
    }}>
      <div
        role="dialog"
        aria-modal="true"
        aria-label={request.title || t('askUser.title')}
        style={{
        background: 'var(--color-surface)',
        borderRadius: 'var(--radius-lg)',
        border: '1px solid var(--color-border)',
        width: 540,
        maxWidth: '90vw',
        maxHeight: '85vh',
        boxShadow: '0 20px 60px rgba(0,0,0,0.4)',
        display: 'flex', flexDirection: 'column',
        overflow: 'hidden',
      }}>
        {/* Header */}
        <div style={{
          display: 'flex', alignItems: 'center', justifyContent: 'space-between',
          padding: '16px 20px',
          borderBottom: '1px solid var(--color-border)',
        }}>
          <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
            <HelpCircle size={20} style={{ color: 'var(--color-primary)', flexShrink: 0 }} />
            <span style={{ fontWeight: 700, fontSize: 15, color: 'var(--text-primary)' }}>
              {request.title || t('askUser.title')}
            </span>
          </div>
          <button onClick={handleCancel} aria-label="Close dialog" title="Cancel" style={{
            background: 'none', border: 'none', cursor: 'pointer',
            color: 'var(--text-tertiary)', padding: 4,
          }}><X size={18} /></button>
        </div>

        {/* Questions */}
        <div style={{
          flex: 1, minHeight: 0, overflowY: 'auto',
          padding: '16px 20px',
          display: 'flex', flexDirection: 'column', gap: 20,
        }}>
          {request.questions.map((q, qi) => (
            <div key={q.id} style={{
              display: 'flex', flexDirection: 'column',
              paddingBottom: qi < request.questions.length - 1 ? 16 : 0,
              borderBottom: qi < request.questions.length - 1 ? '1px solid var(--color-border)' : 'none',
            }}>
              <div style={labelStyle}>{q.title}</div>
              {q.prompt && <div style={promptStyle}>{q.prompt}</div>}

              {/* Single choice - radio buttons */}
              {q.kind === 'single' && q.choices.map(choice => {
                const isSelected = answers[q.id]?.selectedChoiceIds.includes(choice.id)
                return (
                  <div
                    key={choice.id}
                    onClick={() => handleSingleSelect(q.id, choice.id)}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 10,
                      padding: '6px 0', cursor: 'pointer',
                    }}
                  >
                    <div style={{
                      ...radioCheckStyle,
                      borderColor: isSelected ? 'var(--color-primary)' : 'var(--color-border)',
                      background: isSelected ? 'var(--color-primary)' : 'transparent',
                    }}>
                      {isSelected && <div style={{
                        width: 6, height: 6, borderRadius: '50%', background: '#fff',
                      }} />}
                    </div>
                    <span style={{
                      fontSize: 13, color: isSelected ? 'var(--text-primary)' : 'var(--text-secondary)',
                    }}>{choice.label}</span>
                  </div>
                )
              })}

              {/* Multi choice - checkboxes */}
              {q.kind === 'multi' && q.choices.map(choice => {
                const isSelected = answers[q.id]?.selectedChoiceIds.includes(choice.id)
                return (
                  <div
                    key={choice.id}
                    onClick={() => handleMultiToggle(q.id, choice.id)}
                    style={{
                      display: 'flex', alignItems: 'center', gap: 10,
                      padding: '6px 0', cursor: 'pointer',
                    }}
                  >
                    <div style={{
                      ...checkboxStyle,
                      borderColor: isSelected ? 'var(--color-primary)' : 'var(--color-border)',
                      background: isSelected ? 'var(--color-primary)' : 'transparent',
                    }}>
                      {isSelected && (
                        <svg width="10" height="8" viewBox="0 0 10 8" fill="none">
                          <path d="M1 4L3.5 6.5L9 1" stroke="#fff" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"/>
                        </svg>
                      )}
                    </div>
                    <span style={{
                      fontSize: 13, color: isSelected ? 'var(--text-primary)' : 'var(--text-secondary)',
                    }}>{choice.label}</span>
                  </div>
                )
              })}

              {/* Freeform text input */}
              {(q.kind === 'text' || q.allow_freeform) && (
                <textarea
                  value={answers[q.id]?.freeformText || ''}
                  onChange={e => updateAnswer(q.id, { freeformText: e.target.value })}
                  placeholder={q.kind === 'text' ? 'Type your answer...' : 'Additional notes (optional)'}
                  rows={q.kind === 'text' ? 3 : 2}
                  style={{
                    ...inputStyle,
                    resize: 'vertical',
                    marginTop: (q.kind === 'single' || q.kind === 'multi') && q.choices.length > 0 ? 8 : 0,
                  }}
                />
              )}
            </div>
          ))}
        </div>

        {/* Buttons */}
        <div style={{
          display: 'flex', gap: 10, justifyContent: 'flex-end',
          padding: '12px 20px 16px',
          borderTop: '1px solid var(--color-border)',
        }}>
          <button
            onClick={handleCancel}
            disabled={responding}
            aria-label="Cancel"
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-card)', color: 'var(--text-secondary)',
              border: '1px solid var(--color-border)',
              cursor: responding ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 13,
              opacity: responding ? 0.5 : 1,
            }}
          >
            {t('im.cancel')}
          </button>
          <button
            onClick={handleSubmit}
            disabled={responding}
            aria-label={t('askUser.submit')}
            style={{
              padding: '8px 20px', borderRadius: 'var(--radius-md)',
              background: 'var(--color-primary)', color: '#fff',
              border: 'none',
              cursor: responding ? 'not-allowed' : 'pointer',
              fontWeight: 600, fontSize: 13,
              display: 'flex', alignItems: 'center', gap: 6,
              opacity: responding ? 0.5 : 1,
            }}
          >
            <Send size={14} /> {t('askUser.submit')}
          </button>
        </div>
      </div>
    </div>
  )
}
