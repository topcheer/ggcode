import React, { useEffect, useRef, useState } from 'react';
import { Send, Plus, Settings } from 'lucide-react';
import { MessageBubble } from './components/MessageBubble';
import { useChatStore } from './store';
import './App.css';

// Wails bindings
// @ts-ignore - Wails auto-generated bindings
import * as ChatService from '../bindings/github.com/topcheer/ggcode/desktop/chatservice.js';

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
  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

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

  // Auto-scroll to bottom on new messages
  useEffect(() => {
    messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' });
  }, [messages]);

  // Auto-resize textarea
  const handleInput = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setInput(e.target.value);
    const ta = e.target;
    ta.style.height = 'auto';
    ta.style.height = Math.min(ta.scrollHeight, 160) + 'px';
  };

  const handleSend = async () => {
    if (!input.trim() || isStreaming) return;

    const userMessage = {
      role: 'user' as const,
      content: [{ type: 'text' as const, text: input.trim() }],
    };
    addMessage(userMessage);
    setInput('');
    if (textareaRef.current) {
      textareaRef.current.style.height = 'auto';
    }

    // Create assistant message placeholder
    const assistantMessage = {
      role: 'assistant' as const,
      content: [] as any[],
    };
    addMessage(assistantMessage);
    setStreaming(true);

    // TODO: Connect to actual chat backend via Wails events
    // For now, simulate a response for demo
    setTimeout(() => {
      appendToLastAssistant({
        type: 'text',
        text: 'Hello! I am ggcode, your AI coding assistant. The desktop app is being initialized.\n\nOnce connected to the backend, I will be able to:\n- Read and edit your code\n- Run commands\n- Search your codebase\n- And much more!\n\n```go\nfmt.Println("Welcome to ggcode desktop!")\n```',
      });
      appendToLastAssistant({
        type: 'tool_call_done',
        tool: {
          id: 'demo-1',
          name: 'read_file',
          input: { path: '/src/main.go' },
        },
      });
      appendToLastAssistant({
        type: 'tool_result',
        tool: { id: 'demo-1', name: 'read_file' },
        result: 'package main\n\nimport "fmt"\n\nfunc main() {\n\tfmt.Println("Hello, World!")\n}',
      });
      appendToLastAssistant({
        type: 'text',
        text: '\nI found your `main.go` file. It looks like a simple Hello World program. Would you like me to help you modify it?',
      });
      appendToLastAssistant({
        type: 'done',
        usage: { input_tokens: 150, output_tokens: 200, cache_read_tokens: 0, cache_write_tokens: 0 },
      });
    }, 500);
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  // Get current vendor/endpoint/model display names
  const currentVendor = vendors.find((v) => v.name === activeProvider.vendor);
  const currentEndpoint = currentVendor?.endpoints.find(
    (ep) => ep.name === activeProvider.endpoint
  );

  return (
    <div className="app-layout">
      {/* ─── Left Sidebar: Session History ─── */}
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

      {/* ─── Main Chat Area ─── */}
      <div className="chat-area">
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
                <div className="shortcut">
                  <kbd>Cmd+Shift+G</kbd> Toggle window
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
            <button
              className="send-btn"
              onClick={handleSend}
              disabled={!input.trim() || isStreaming}
            >
              <Send size={14} />
            </button>
          </div>
        </div>
      </div>

      {/* ─── Right Sidebar: Context Panel ─── */}
      <div className="context-panel">
        <h3>Context</h3>

        <div className="context-section">
          <h4>Token Usage</h4>
          <div className="token-stats">
            <span>In: {totalInputTokens.toLocaleString()}</span>
            <span>Out: {totalOutputTokens.toLocaleString()}</span>
          </div>
          <div className="usage-bar" style={{ marginTop: 8 }}>
            <div
              className="usage-bar-fill"
              style={{ width: '0%' }}
            />
          </div>
        </div>

        <div className="context-section">
          <h4>Attached Files</h4>
          <div className="file-item" style={{ color: 'var(--text-muted)' }}>
            No files attached
          </div>
        </div>
      </div>

      {/* ─── Bottom Bar: Provider Selection ─── */}
      <div className="provider-bar">
        <span className="provider-label">Provider</span>
        <select className="provider-select" value={activeProvider.vendor}>
          {vendors.map((v) => (
            <option key={v.name} value={v.name}>
              {v.displayName}
            </option>
          ))}
        </select>

        {currentVendor && (
          <>
            <div className="provider-divider" />
            <select className="provider-select" value={activeProvider.endpoint}>
              {currentVendor.endpoints.map((ep) => (
                <option key={ep.name} value={ep.name}>
                  {ep.displayName || ep.name}
                </option>
              ))}
            </select>
          </>
        )}

        {currentEndpoint && (
          <>
            <div className="provider-divider" />
            <select className="provider-select" value={activeProvider.model}>
              {currentEndpoint.models.map((m) => (
                <option key={m} value={m}>
                  {m}
                </option>
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
