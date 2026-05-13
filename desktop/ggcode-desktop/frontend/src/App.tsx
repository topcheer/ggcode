import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Send, Plus, Settings, PanelLeftClose, PanelRightClose } from 'lucide-react';
import { MessageBubble } from './components/MessageBubble';
import { useChatStore } from './store';
import './App.css';

// @ts-ignore - Wails auto-generated bindings
import * as ChatService from '../bindings/github.com/topcheer/ggcode/desktop/chatservice.js';
// @ts-ignore
import { Events } from '@wailsio/runtime';

function App() {
  const {
    messages,
    isStreaming,
    vendors,
    activeProvider,
    totalInputTokens,
    totalOutputTokens,
    addMessage,
    appendToLastAssistant,
    setStreaming,
    setVendors,
    setActiveProvider,
    clearMessages,
  } = useChatStore();

  const [input, setInput] = useState('');
  const [leftPanelOpen, setLeftPanelOpen] = useState(true);
  const [rightPanelOpen, setRightPanelOpen] = useState(true);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Listen for Wails streaming events
  useEffect(() => {
    // @ts-ignore - Wails event callback
    const unregister = Events.On('ggcode:chat:stream', (ev: any) => {
      const data = typeof ev === 'string' ? ev : ev?.data;
      if (!data) return;
      try {
        const event = JSON.parse(data);
        appendToLastAssistant(event);
      } catch (e) {
        console.error('Failed to parse stream event:', e);
      }
    });
    return () => {
      if (typeof unregister === 'function') unregister();
    };
  }, [appendToLastAssistant]);

  // Load vendors on mount
  useEffect(() => {
    async function loadVendors() {
      try {
        const v = await ChatService.GetVendors();
        if (v) setVendors(v as any);
        const a = await ChatService.GetActiveProvider();
        if (a) setActiveProvider(a as any);
      } catch (e) {
        console.error('Failed to load vendors:', e);
      }
    }
    loadVendors();
  }, []);

  // Auto-scroll to bottom
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  const handleInput = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value);
    const ta = e.target;
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 160) + 'px';
  };

  const handleSend = useCallback(async () => {
    if (!input.trim() || isStreaming) return;

    const userText = input.trim();
    addMessage({
      role: 'user',
      content: [{ type: 'text', text: userText }],
    });
    setInput('');
    if (textareaRef.current) textareaRef.current.style.height = 'auto';

    // Create assistant placeholder
    addMessage({ role: 'assistant', content: [] });
    setStreaming(true);

    try {
      await ChatService.SendMessage(userText);
    } catch (e) {
      console.error('SendMessage error:', e);
      appendToLastAssistant({
        type: 'error',
        error: String(e),
      });
    }
  }, [input, isStreaming, addMessage, setStreaming, appendToLastAssistant]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  const handleProviderChange = async (vendor: string, endpoint: string, model: string) => {
    try {
      await ChatService.SetActiveProvider(vendor, endpoint, model);
      setActiveProvider({ vendor, endpoint, model });
      clearMessages();
    } catch (e) {
      console.error('SetActiveProvider error:', e);
    }
  };

  const currentVendor = vendors.find((v) => v.name === activeProvider.vendor);
  const currentEndpoint = currentVendor?.endpoints.find(
    (ep) => ep.name === activeProvider.endpoint
  );

  return (
    <div className="app-layout" style={{
      gridTemplateColumns: `${leftPanelOpen ? '260px' : '0px'} 1fr ${rightPanelOpen ? '280px' : '0px'}`
    }}>
      {/* ─── Left Sidebar: Session History ─── */}
      {leftPanelOpen && (
        <div className="sidebar">
          <div className="sidebar-header">
            <h2>ggcode</h2>
            <button className="new-chat-btn" onClick={clearMessages}>
              <Plus size={14} />
            </button>
          </div>
          <div className="session-list">
            <div className="session-item active">Current Session</div>
          </div>
        </div>
      )}

      {/* ─── Main Chat Area ─── */}
      <div className="chat-area">
        {/* Toolbar */}
        <div style={{ display: 'flex', alignItems: 'center', padding: '8px 16px', borderBottom: '1px solid var(--border)', gap: 8 }}>
          <button className="toolbar-btn" onClick={() => setLeftPanelOpen(!leftPanelOpen)} title="Toggle sidebar">
            <PanelLeftClose size={16} />
          </button>
          <span style={{ fontSize: 13, color: 'var(--text-muted)' }}>
            {currentVendor?.displayName || activeProvider.vendor}
            {currentEndpoint && ` / ${currentEndpoint.displayName || currentEndpoint.name}`}
            {activeProvider.model && ` / ${activeProvider.model}`}
          </span>
          <div style={{ flex: 1 }} />
          <button className="toolbar-btn" onClick={() => setRightPanelOpen(!rightPanelOpen)} title="Toggle context panel">
            <PanelRightClose size={16} />
          </button>
        </div>

        <div className="messages-container">
          {messages.length === 0 ? (
            <div className="empty-state">
              <div className="logo">ggcode</div>
              <p>Your AI coding assistant</p>
              <div className="shortcuts">
                <div className="shortcut">
                  <kbd>Enter</kbd> Send message
                </div>
                <div className="shortcut">
                  <kbd>Shift+Enter</kbd> New line
                </div>
              </div>
            </div>
          ) : (
            messages.map((msg, i) => (
              <MessageBubble
                key={i}
                role={msg.role}
                content={msg.content}
                messageIndex={i}
              />
            ))
          )}
          {isStreaming && (
            <div style={{ padding: '8px 0' }}>
              <span className="streaming-dot" />
            </div>
          )}
          <div ref={messagesEndRef} />
        </div>

        <div className="input-area">
          <div className="input-wrapper">
            <textarea
              ref={textareaRef}
              className="chat-input"
              value={input}
              onChange={handleInput}
              onKeyDown={handleKeyDown}
              placeholder="Message ggcode..."
              rows={1}
              disabled={isStreaming}
            />
            <button className="send-btn" onClick={handleSend} disabled={!input.trim() || isStreaming}>
              <Send size={14} />
            </button>
          </div>
        </div>
      </div>

      {/* ─── Right Sidebar: Context Panel ─── */}
      {rightPanelOpen && (
        <div className="context-panel">
          <h3>Context</h3>
          <div className="context-section">
            <h4>Token Usage</h4>
            <div className="token-stats">
              <span>In: {totalInputTokens.toLocaleString()}</span>
              <span>Out: {totalOutputTokens.toLocaleString()}</span>
            </div>
          </div>
          <div className="context-section">
            <h4>Attached Files</h4>
            <div className="file-item" style={{ color: 'var(--text-muted)' }}>No files attached</div>
          </div>
        </div>
      )}

      {/* ─── Bottom Bar: Provider Selection ─── */}
      <div className="provider-bar">
        <span className="provider-label">Provider</span>
        <select
          className="provider-select"
          value={activeProvider.vendor}
          onChange={(e) => {
            const v = vendors.find((v) => v.name === e.target.value);
            const ep = v?.endpoints[0];
            const model = ep?.selectedModel || ep?.models?.[0] || '';
            handleProviderChange(e.target.value, ep?.name || '', model);
          }}
        >
          {vendors.map((v) => (
            <option key={v.name} value={v.name}>{v.displayName}</option>
          ))}
        </select>

        {currentVendor && (
          <>
            <div className="provider-divider" />
            <select
              className="provider-select"
              value={activeProvider.endpoint}
              onChange={(e) => {
                const ep = currentVendor.endpoints.find((ep) => ep.name === e.target.value);
                const model = ep?.selectedModel || ep?.models?.[0] || '';
                handleProviderChange(activeProvider.vendor, e.target.value, model);
              }}
            >
              {currentVendor.endpoints.map((ep) => (
                <option key={ep.name} value={ep.name}>{ep.displayName || ep.name}</option>
              ))}
            </select>
          </>
        )}

        {currentEndpoint && (
          <>
            <div className="provider-divider" />
            <select
              className="provider-select"
              value={activeProvider.model}
              onChange={(e) => handleProviderChange(activeProvider.vendor, activeProvider.endpoint, e.target.value)}
            >
              {currentEndpoint.models.map((m) => (
                <option key={m} value={m}>{m}</option>
              ))}
            </select>
          </>
        )}

        <div className="provider-info">
          <Settings size={14} style={{ cursor: 'pointer', opacity: 0.5 }} />
        </div>
      </div>
    </div>
  );
}

export default App;
