// @vitest-environment jsdom
import { describe, expect, it, afterEach } from 'vitest'
import { render, screen, act, cleanup } from '@testing-library/react'
import { ToastContainer, type ToastMessage } from './Toast'

afterEach(cleanup)

function makeToast(overrides: Partial<ToastMessage> = {}): ToastMessage {
  return { id: 1, type: 'info', message: 'test message', ...overrides }
}

describe('ToastContainer', () => {
  it('returns null when no toasts', () => {
    const { container } = render(<ToastContainer toasts={[]} onDismiss={() => {}} />)
    expect(container.firstChild).toBeNull()
  })

  it('renders a single toast message', () => {
    render(<ToastContainer toasts={[makeToast()]} onDismiss={() => {}} />)
    expect(screen.getByText('test message')).toBeDefined()
  })

  it('renders multiple toasts', () => {
    const toasts = [
      makeToast({ id: 1, message: 'first' }),
      makeToast({ id: 2, message: 'second' }),
      makeToast({ id: 3, message: 'third' }),
    ]
    render(<ToastContainer toasts={toasts} onDismiss={() => {}} />)
    expect(screen.getByText('first')).toBeDefined()
    expect(screen.getByText('second')).toBeDefined()
    expect(screen.getByText('third')).toBeDefined()
  })

  it('limits to MAX_VISIBLE (4) toasts', () => {
    const toasts = Array.from({ length: 6 }, (_, i) =>
      makeToast({ id: i + 1, message: `toast-${i + 1}` })
    )
    render(<ToastContainer toasts={toasts} onDismiss={() => {}} />)
    // Only last 4 should be visible (toast-3 through toast-6)
    expect(screen.queryByText('toast-1')).toBeNull()
    expect(screen.queryByText('toast-2')).toBeNull()
    expect(screen.getByText('toast-3')).toBeDefined()
    expect(screen.getByText('toast-6')).toBeDefined()
  })

  it('has dismiss button with aria-label', () => {
    render(<ToastContainer toasts={[makeToast()]} onDismiss={() => {}} />)
    const dismissBtns = screen.getAllByLabelText('Dismiss notification')
    expect(dismissBtns.length).toBeGreaterThanOrEqual(1)
  })

  it('calls onDismiss when dismiss button clicked', () => {
    let dismissedId = -1
    render(
      <ToastContainer
        toasts={[makeToast({ id: 42 })]}
        onDismiss={(id) => { dismissedId = id }}
      />
    )
    const dismissBtn = screen.getAllByLabelText('Dismiss notification')[0]
    act(() => { dismissBtn.click() })
    expect(dismissedId).toBe(42)
  })

  it('toast has role="status" for screen readers', () => {
    render(<ToastContainer toasts={[makeToast()]} onDismiss={() => {}} />)
    const statuses = screen.getAllByRole('status')
    expect(statuses.length).toBeGreaterThanOrEqual(1)
  })

  it('toast has aria-live="polite"', () => {
    render(<ToastContainer toasts={[makeToast()]} onDismiss={() => {}} />)
    const statuses = screen.getAllByRole('status')
    expect(statuses[0].getAttribute('aria-live')).toBe('polite')
  })

  it('renders success toast type', () => {
    render(
      <ToastContainer
        toasts={[makeToast({ type: 'success', message: 'done!' })]}
        onDismiss={() => {}}
      />
    )
    expect(screen.getByText('done!')).toBeDefined()
  })

  it('renders error toast type', () => {
    render(
      <ToastContainer
        toasts={[makeToast({ type: 'error', message: 'failed!' })]}
        onDismiss={() => {}}
      />
    )
    expect(screen.getByText('failed!')).toBeDefined()
  })
})
