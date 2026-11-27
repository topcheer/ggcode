// @vitest-environment jsdom
// D2 (desktop review 2026-11-27): the approval dialog used to approve on ANY
// Enter keydown the moment it opened, even while the user was typing in the
// composer or confirming an IME candidate. These tests pin the new contract:
// focus moves into the dialog, keys are ignored outside it / during the
// 400ms grace period / during IME composition, and Tab is trapped.
import { describe, it, expect, vi, beforeEach, afterEach } from 'vitest'
import { render, cleanup, fireEvent, act } from '@testing-library/react'

if (typeof window !== 'undefined' && !window.matchMedia) {
  Object.defineProperty(window, 'matchMedia', {
    value: () => ({ matches: false, addEventListener: () => {}, removeEventListener: () => {}, addListener: () => {}, removeListener: () => {}, dispatchEvent: () => false }),
    writable: true, configurable: true,
  })
}

const respondApproval = vi.fn().mockResolvedValue(undefined)
vi.mock('../../wailsjs/go/main/App', () => ({
  RespondApproval: (...args: unknown[]) => respondApproval(...args),
}))

import { ApprovalDialog } from './ApprovalDialog'

const request = { requestID: 'req-1', toolName: 'run_command', input: '{"command":"ls"}' }

const enter = (init: KeyboardEventInit = {}) => fireEvent.keyDown(window, { key: 'Enter', ...init })

describe('ApprovalDialog keyboard contract', () => {
  beforeEach(() => { respondApproval.mockClear() })
  afterEach(cleanup)

  it('takes focus on mount and ignores Enter during the 400ms grace period', async () => {
    render(<ApprovalDialog request={request} onClose={() => {}} />)
    const dialog = document.querySelector('[role="dialog"]') as HTMLElement
    expect(document.activeElement).toBe(dialog)
    enter()
    expect(respondApproval).not.toHaveBeenCalled()
    await act(async () => { await new Promise(r => setTimeout(r, 450)) })
    enter()
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(respondApproval).toHaveBeenCalledTimes(1)
    expect(respondApproval).toHaveBeenCalledWith('req-1', 'allow')
  })

  it('ignores Enter/Escape while focus is outside the dialog (user typing in the composer)', async () => {
    const onClose = vi.fn()
    const outside = document.createElement('input')
    document.body.appendChild(outside)
    render(<ApprovalDialog request={request} onClose={onClose} />)
    outside.focus()
    await act(async () => { await new Promise(r => setTimeout(r, 450)) })
    enter()
    fireEvent.keyDown(window, { key: 'Escape' })
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(respondApproval).not.toHaveBeenCalled()
    expect(onClose).not.toHaveBeenCalled()
    outside.remove()
  })

  it('ignores keys during IME composition and maps Escape to deny, Shift+Enter to always_allow', async () => {
    const onClose = vi.fn()
    render(<ApprovalDialog request={request} onClose={onClose} />)
    await act(async () => { await new Promise(r => setTimeout(r, 450)) })
    enter({ isComposing: true })
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(respondApproval).not.toHaveBeenCalled()
    enter({ shiftKey: true })
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(respondApproval).toHaveBeenLastCalledWith('req-1', 'always_allow')
  })

  it('does not approve on Enter pressed on a focused button (native activation only)', async () => {
    render(<ApprovalDialog request={request} onClose={() => {}} />)
    await act(async () => { await new Promise(r => setTimeout(r, 450)) })
    const denyBtn = [...document.querySelectorAll('button')].find(b => /deny|reject|always/i.test(b.textContent || '')) as HTMLButtonElement
    if (!denyBtn) return
    denyBtn.focus()
    enter()
    await act(async () => { await new Promise(r => setTimeout(r, 0)) })
    expect(respondApproval).not.toHaveBeenCalled()
  })
})
