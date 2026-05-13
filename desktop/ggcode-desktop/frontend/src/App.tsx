import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Send, Plus, Settings, PanelLeftClose, PanelRightClose, FileText, X } from 'lucide-react';
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
  const [isDragOver, setIsDragOver] = useState(false);
  const [attachedFiles, setAttachedFiles] = useState<string[]>([]);
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const dragCounterRef = useRef(0);

  // Listen for Wails streaming events
  useEffect(() => {
    // @ts-ignore
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
    setAttachedFiles([]);
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

  // ─── Drag & Drop ───────────────────────

  const handleDragEnter = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current++;
    if (e.dataTransfer.types.includes('Files')) {
      setIsDragOver(true);
    }
  };

  const handleDragLeave = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    dragCounterRef.current--;
    if (dragCounterRef.current === 0) {
      setIsDragOver(false);
    }
  };

  const handleDragOver = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
  };

  const handleDrop = (e: React.DragEvent) => {
    e.preventDefault();
    e.stopPropagation();
    setIsDragOver(false);
    dragCounterRef.current = 0;

    const files = e.dataTransfer.files;
    if (files.length > 0) {
      const names = Array.from(files).map((f) => f.name);
      setAttachedFiles((prev) => [...new Set([...prev, ...names])]);
    }
  };

  const removeAttachedFile = (name: string) => {
    setAttachedFiles((prev) => prev.filter((f) => f !== name));
  };

  const currentVendor = vendors.find((v) => v.name === activeProvider.vendor);
  const currentEndpoint = currentVendor?.endpoints.find(
    (ep) => ep.name === activeProvider.endpoint
  );

  return (
    <div
      className="app-layout"
      style={{ gridTemplateColumns: `${leftPanelOpen ? '260px' : '0px'} 1fr ${rightPanelOpen ? '280px' : '0px'}` }}
      onDragEnter={handleDragEnter}
      onDragLeave={handleDragLeave}
      onDragOver={handleDragOver}
      onDrop={handleDrop}
    >
      {/* Drop zone overlay */}
      {isDragOver && (
        <div className="drop-zone-overlay">
          <div className="drop-zone-text">Drop files here to attach</div>
        </div>
      )}

      {/* ─── Left Sidebar ─── */}
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
                <div className="shortcut"><kbd>Enter</kbd> Send message</div>
                <div className="shortcut"><kbd>Shift+Enter</kbd> New line</div>
                <div className="shortcut"><kbd>Cmd+N</kbd> New chat</div>
                <div className="shortcut"><kbd>Drag files</kbd> Attach to context</div>
              </div>
            </div>
          ) : (
            messages.map((msg, i) => (
              <MessageBubble key={i} role={msg.role} content={msg.content} messageIndex={i} />
            ))
          )}
          {isStreaming && (
            <div style={{ padding: '8px 0' }}><span className="streaming-dot" /></div>
          )}
          <div ref={messagesEndRef} />
        </div>

        <div className="input-area">
          {attachedFiles.length > 0 && (
            <div className="input-attachments">
              {attachedFiles.map((f) => (
                <span key={f} className="attached-file">
                  <FileText size={12} />
                  {f}
                  <span className="remove-file" onClick={() => removeAttachedFile(f)}><X size={12} /></span>
                </span>
              ))}
            </div>
          )}
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

      {/* ─── Right Sidebar ─── */}
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
          {attachedFiles.length > 0 && (
            <div className="context-section">
              <h4>Attached Files ({attachedFiles.length})</h4>
              {attachedFiles.map((f) => (
                <div key={f} className="file-item">
                  <FileText size={12} style={{ display: 'inline', marginRight: 6 }} />
                  {f}
                </div>
              ))}
            </div>
          )}
          <div className="context-section">
            <h4>Workspace</h4>
            <div className="file-item" style={{ color: 'var(--text-muted)' }}>
              No files attached
            </div>
          </div>
        </div>
      )}

      {/* ─── Bottom Bar ─── */}
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
