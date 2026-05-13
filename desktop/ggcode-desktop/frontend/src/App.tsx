import React, { useEffect, useRef, useState, useCallback } from 'react';
import { Send, Plus, PanelLeftClose, PanelRightClose, X,
  MessageSquare, Radio, Sliders, RefreshCw, Bot, ChevronRight } from 'lucide-react';
import { MessageBubble } from './components/MessageBubble';
import { useChatStore } from './store';
import './App.css';

// @ts-ignore
import * as ChatService from '../bindings/github.com/topcheer/ggcode/desktop/chatservice.js';
// @ts-ignore
import { Events } from '@wailsio/runtime';

type AppPhase = 'loading' | 'welcome' | 'onboard' | 'chat';
type RightTab = 'context' | 'provider' | 'im' | 'settings';
type OnboardStep = 'language' | 'vendor' | 'endpoint' | 'model' | 'optional' | 'im';

function App() {
  const [phase, setPhase] = useState<AppPhase>('loading');
  const [onboardStep, setOnboardStep] = useState<OnboardStep>('language');
  const [workDirInput, setWorkDirInput] = useState('');
  const [workDirError, setWorkDirError] = useState('');

  // Onboard state
  const [presets, setPresets] = useState<any[]>([]);
  const [selectedVendor, setSelectedVendor] = useState<any>(null);
  const [selectedEndpointIdx, setSelectedEndpointIdx] = useState(0);
  const [apiKey, setApiKey] = useState('');
  const [models, setModels] = useState<string[]>([]);
  const [selectedModelIdx, setSelectedModelIdx] = useState(0);
  const [modelsLoading, setModelsLoading] = useState(false);
  const [selectedMode, setSelectedMode] = useState('supervised');
  const [knightEnabled, setKnightEnabled] = useState(false);
  const [a2aEnabled, setA2AEnabled] = useState(false);

  // Chat state
  const {
    messages, isStreaming, vendors, activeProvider,
    totalInputTokens, totalOutputTokens,
    addMessage, appendToLastAssistant, setStreaming,
    setVendors, setActiveProvider, clearMessages,
  } = useChatStore();

  const [input, setInput] = useState('');
  const [leftPanelOpen, setLeftPanelOpen] = useState(true);
  const [rightPanelOpen, setRightPanelOpen] = useState(true);
  const [sessions, setSessions] = useState<any[]>([]);
  const [rightTab, setRightTab] = useState<RightTab>('context');
  const [settings, setSettings] = useState<any>(null);
  const [imAdapters, setIMAdapters] = useState<any[]>([]);
  const [updateInfo, setUpdateInfo] = useState<any>(null);
  const [theme, setTheme] = useState('system');
  const [fontSize, setFontSize] = useState(14);

  const messagesEndRef = useRef<HTMLDivElement>(null);
  const textareaRef = useRef<HTMLTextAreaElement>(null);

  // Theme & font
  useEffect(() => {
    document.documentElement.setAttribute('data-theme', theme === 'system' ? '' : theme);
    if (theme === 'system') document.documentElement.removeAttribute('data-theme');
    localStorage.setItem('ggcode-theme', theme);
  }, [theme]);
  useEffect(() => {
    document.documentElement.style.fontSize = fontSize + 'px';
    localStorage.setItem('ggcode-fontsize', String(fontSize));
  }, [fontSize]);

  // Wails streaming events
  useEffect(() => {
    // @ts-ignore
    const unregister = Events.On('ggcode:chat:stream', (ev: any) => {
      const data = typeof ev === 'string' ? ev : ev?.data;
      if (!data) return;
      try { appendToLastAssistant(JSON.parse(data)); } catch {}
    });
    return () => { if (typeof unregister === 'function') unregister(); };
  }, [appendToLastAssistant]);

  // ─── Startup flow ────────────────────────────
  useEffect(() => {
    (async () => {
      const desktopCfg = await ChatService.GetDesktopConfig();
      if (desktopCfg?.theme) setTheme(desktopCfg.theme);
      if (desktopCfg?.fontSize) setFontSize(desktopCfg.fontSize);

      if (!desktopCfg?.workDir) {
        setPhase('welcome');
        return;
      }

      // WorkDir was auto-loaded by main.go. Check if onboard needed.
      const needs = await ChatService.NeedsOnboard();
      if (needs) {
        const p = await ChatService.GetVendorPresets();
        setPresets(p || []);
        setPhase('onboard');
        setOnboardStep('vendor');
      } else {
        await enterChat();
      }
    })();
  }, []);

  const enterChat = async () => {
    const v = await ChatService.GetVendors(); if (v) setVendors(v);
    const a = await ChatService.GetActiveProvider(); if (a) setActiveProvider(a);
    const s = await ChatService.GetSessions();
    setSessions((s || []).map((ses: any) => ({
      id: ses.ID || ses.id,
      title: ses.Title || ses.title || 'Untitled',
      updatedAt: ses.UpdatedAt || ses.updatedAt || '',
      workspace: ses.Workspace || ses.workspace || '',
      vendor: ses.Vendor || ses.vendor || '',
      model: ses.Model || ses.model || '',
      msgCount: ses.MsgCount || ses.msgCount || 0,
    })));
    const g = await ChatService.GetSettings(); if (g) setSettings(g);
    const im = await ChatService.GetIMAdapters(); if (im) setIMAdapters(im);
    setPhase('chat');
  };

  useEffect(() => { messagesEndRef.current?.scrollIntoView({ behavior: 'smooth' }); }, [messages]);

  // ─── Welcome: Set WorkDir ────────────────────
  const handleSetWorkDir = async () => {
    setWorkDirError('');
    try {
      await ChatService.SetWorkDir(workDirInput.trim());
      const needs = await ChatService.NeedsOnboard();
      if (needs) {
        const p = await ChatService.GetVendorPresets();
        setPresets(p || []);
        setPhase('onboard');
        setOnboardStep('vendor');
      } else {
        await enterChat();
      }
    } catch (e: any) {
      setWorkDirError(e?.message || String(e));
    }
  };

  // ─── Onboard ─────────────────────────────────
  const handleSelectVendor = async (preset: any) => {
    setSelectedVendor(preset);
    setSelectedEndpointIdx(0);
    setApiKey('');
    setOnboardStep('endpoint');
  };

  const handleEndpointNext = async () => {
    setOnboardStep('model');
    setModelsLoading(true);
    try {
      // Temporarily set provider so DiscoverModels can resolve
      const ep = selectedVendor.Endpoints[selectedEndpointIdx];
      await ChatService.SetActiveProvider(selectedVendor.ID, ep?.ID || '', ep?.DefaultModel || '');
      const m = await ChatService.DiscoverModels();
      setModels(m && m.length > 0 ? m : (ep?.Models?.length > 0 ? ep.Models : [ep?.DefaultModel || 'default']));
      setSelectedModelIdx(0);
    } catch {
      const ep = selectedVendor.Endpoints[selectedEndpointIdx];
      setModels(ep?.Models?.length > 0 ? ep.Models : [ep?.DefaultModel || 'default']);
      setSelectedModelIdx(0);
    }
    setModelsLoading(false);
  };

  const handleCompleteOnboard = async () => {
    const ep = selectedVendor.Endpoints[selectedEndpointIdx];
    const model = models[selectedModelIdx] || ep?.DefaultModel || '';
    try {
      await ChatService.CompleteOnboard(
        'en', selectedVendor.ID, ep?.ID || '', apiKey, model,
        selectedMode, knightEnabled, a2aEnabled, {}
      );
      await enterChat();
    } catch (e: any) {
      console.error('Onboard failed:', e);
    }
  };

  // ─── Chat ────────────────────────────────────
  const handleSend = useCallback(async () => {
    if (!input.trim() || isStreaming) return;
    const text = input.trim();
    addMessage({ role: 'user', content: [{ type: 'text', text }] });
    setInput('');
    if (textareaRef.current) textareaRef.current.style.height = 'auto';
    addMessage({ role: 'assistant', content: [] });
    setStreaming(true);
    try { await ChatService.SendMessage(text); }
    catch (e) { appendToLastAssistant({ type: 'error', error: String(e) }); }
  }, [input, isStreaming, addMessage, setStreaming, appendToLastAssistant]);

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Enter' && !e.shiftKey) { e.preventDefault(); handleSend(); }
  };

  const handleProviderChange = async (vendor: string, endpoint: string, model: string) => {
    await ChatService.SetActiveProvider(vendor, endpoint, model);
    setActiveProvider({ vendor, endpoint, model });
  };

  const currentVendor = vendors.find(v => v.name === activeProvider.vendor);
  const currentEndpoint = currentVendor?.endpoints.find(ep => ep.name === activeProvider.endpoint);

  // ─── Render ──────────────────────────────────

  // Loading
  if (phase === 'loading') {
    return <div className="app-loading"><div className="logo">ggcode</div><p>Loading...</p></div>;
  }

  // Welcome: choose workspace
  if (phase === 'welcome') {
    return (
      <div className="onboard-container">
        <div className="onboard-card">
          <h1>Welcome to ggcode</h1>
          <p>Select your project directory to get started.</p>
          <div style={{ marginTop: 24 }}>
            <label className="config-label">Project Directory</label>
            <div style={{ display: 'flex', gap: 8 }}>
              <input className="config-input" value={workDirInput} onChange={e => setWorkDirInput(e.target.value)}
                placeholder="/path/to/your/project" style={{ flex: 1 }} />
              <button className="config-btn" style={{ width: 'auto', padding: '6px 16px' }} onClick={handleSetWorkDir}>
                Continue
              </button>
            </div>
            {workDirError && <div style={{ color: 'var(--error)', fontSize: 12, marginTop: 4 }}>{workDirError}</div>}
          </div>
        </div>
      </div>
    );
  }

  // Onboard wizard
  if (phase === 'onboard') {
    return (
      <div className="onboard-container">
        <div className="onboard-card">
          <h1>Configure ggcode</h1>

          {onboardStep === 'vendor' && (
            <>
              <p>Select your AI provider:</p>
              <div className="onboard-list">
                {presets.map((p, i) => (
                  <button key={p.ID} className="onboard-item" onClick={() => handleSelectVendor(p)}>
                    <span className="onboard-item-name">{p.DisplayName}</span>
                    <ChevronRight size={14} className="onboard-item-arrow" />
                  </button>
                ))}
              </div>
            </>
          )}

          {onboardStep === 'endpoint' && selectedVendor && (
            <>
              <p>{selectedVendor.DisplayName} — Select endpoint & API key:</p>
              <div style={{ marginTop: 12 }}>
                <label className="config-label">Endpoint</label>
                <select className="config-select" value={selectedEndpointIdx} onChange={e => setSelectedEndpointIdx(parseInt(e.target.value))}>
                  {selectedVendor.Endpoints.map((ep: any, i: number) => (
                    <option key={i} value={i}>{ep.DisplayName}</option>
                  ))}
                </select>
              </div>
              {selectedVendor.NeedsAPIKey && (
                <div style={{ marginTop: 12 }}>
                  <label className="config-label">API Key</label>
                  <input className="config-input" type="password" value={apiKey} onChange={e => setApiKey(e.target.value)}
                    placeholder={selectedVendor.APIKeyEnvHint || 'Enter API key...'} />
                </div>
              )}
              <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
                <button className="config-btn-secondary" onClick={() => setOnboardStep('vendor')}>Back</button>
                <button className="config-btn" onClick={handleEndpointNext}>
                  {modelsLoading ? 'Loading...' : 'Next'}
                </button>
              </div>
            </>
          )}

          {onboardStep === 'model' && (
            <>
              <p>Select model:</p>
              {modelsLoading ? <p>Loading models...</p> : (
                <div className="onboard-list" style={{ maxHeight: 300, overflow: 'auto' }}>
                  {models.map((m, i) => (
                    <button key={i} className={`onboard-item ${i === selectedModelIdx ? 'selected' : ''}`} onClick={() => setSelectedModelIdx(i)}>
                      <span className="onboard-item-name">{m}</span>
                    </button>
                  ))}
                </div>
              )}
              <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
                <button className="config-btn-secondary" onClick={() => setOnboardStep('endpoint')}>Back</button>
                <button className="config-btn" onClick={() => setOnboardStep('optional')}>Next</button>
              </div>
            </>
          )}

          {onboardStep === 'optional' && (
            <>
              <p>Optional settings:</p>
              <div className="onboard-list">
                <div className="onboard-setting">
                  <span>Permission Mode</span>
                  <select className="config-select" style={{ width: 160 }} value={selectedMode} onChange={e => setSelectedMode(e.target.value)}>
                    <option value="supervised">Supervised</option>
                    <option value="auto">Auto</option>
                    <option value="bypass">Bypass</option>
                    <option value="autopilot">Autopilot</option>
                  </select>
                </div>
                <label className="onboard-setting">
                  <span>Enable Knight (background agent)</span>
                  <input type="checkbox" checked={knightEnabled} onChange={e => setKnightEnabled(e.target.checked)} />
                </label>
                <label className="onboard-setting">
                  <span>Enable A2A (agent-to-agent)</span>
                  <input type="checkbox" checked={a2aEnabled} onChange={e => setA2AEnabled(e.target.checked)} />
                </label>
              </div>
              <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
                <button className="config-btn-secondary" onClick={() => setOnboardStep('model')}>Back</button>
                <button className="config-btn" onClick={() => setOnboardStep('im')}>Next</button>
              </div>
            </>
          )}

          {onboardStep === 'im' && (
            <>
              <p>Connect messaging channels (optional, you can skip):</p>
              <p style={{ fontSize: 12, color: 'var(--text-muted)' }}>Configure IM later in the Settings panel.</p>
              <div style={{ display: 'flex', gap: 8, marginTop: 16 }}>
                <button className="config-btn-secondary" onClick={() => setOnboardStep('optional')}>Back</button>
                <button className="config-btn" onClick={handleCompleteOnboard}>Start Using ggcode</button>
              </div>
            </>
          )}
        </div>
      </div>
    );
  }

  // ─── Main Chat UI ────────────────────────────
  return (
    <div className="app-layout" style={{ gridTemplateColumns: `${leftPanelOpen ? '260px' : '0px'} 1fr ${rightPanelOpen ? '320px' : '0px'}` }}>
      {/* Left Sidebar: Sessions */}
      <div className="sidebar" style={!leftPanelOpen ? { width: 0, overflow: 'hidden', borderRight: 'none' } : undefined}>
        <div className="sidebar-header">
          <h2>ggcode</h2>
          <button className="new-chat-btn" onClick={clearMessages}><Plus size={14} /></button>
        </div>
        <div className="session-list">
          <div className="session-item active" style={{ opacity: 0.6 }}>+ New Chat</div>
          {sessions.map(s => (
            <div key={s.id} className="session-item" title={`${s.workspace} — ${s.updatedAt}`}>
              <span style={{ overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', flex: 1 }}>{s.title || 'Untitled'}</span>
              <span style={{ fontSize: 10, color: 'var(--text-muted)', marginLeft: 4, flexShrink: 0 }}>{s.updatedAt}</span>
            </div>
          ))}
        </div>
      </div>

      {/* Chat Area */}
      <div className="chat-area">
        <div style={{ display: 'flex', alignItems: 'center', padding: '8px 16px', borderBottom: '1px solid var(--border)', gap: 8 }}>
          <button className="toolbar-btn" onClick={() => setLeftPanelOpen(!leftPanelOpen)}><PanelLeftClose size={16} /></button>
          <span style={{ fontSize: 13, color: 'var(--text-muted)' }}>
            {currentVendor?.displayName || activeProvider.vendor}
            {currentEndpoint && ` / ${currentEndpoint.displayName || currentEndpoint.name}`}
            {activeProvider.model && ` / ${activeProvider.model}`}
          </span>
          <div style={{ flex: 1 }} />
          <button className="toolbar-btn" onClick={() => setRightPanelOpen(!rightPanelOpen)}><PanelRightClose size={16} /></button>
        </div>

        <div className="messages-container">
          {messages.length === 0 ? (
            <div className="empty-state">
              <div className="logo">ggcode</div>
              <p>Your AI coding assistant</p>
            </div>
          ) : messages.map((msg, i) => <MessageBubble key={i} role={msg.role} content={msg.content} messageIndex={i} />)}
          {isStreaming && <div style={{ padding: '8px 0' }}><span className="streaming-dot" /></div>}
          <div ref={messagesEndRef} />
        </div>

        <div className="input-area">
          <div className="input-wrapper">
            <textarea ref={textareaRef} className="chat-input" value={input}
              onChange={e => { setInput(e.target.value); e.target.style.height = 'auto'; e.target.style.height = Math.min(e.target.scrollHeight, 160) + 'px'; }}
              onKeyDown={handleKeyDown} placeholder="Message ggcode..." rows={1} disabled={isStreaming} />
            <button className="send-btn" onClick={handleSend} disabled={!input.trim() || isStreaming}><Send size={14} /></button>
          </div>
        </div>
      </div>

      {/* Right Sidebar: Tabs */}
      <div className="context-panel" style={!rightPanelOpen ? { width: 0, overflow: 'hidden', borderLeft: 'none' } : undefined}>
        <div className="right-tabs">
          {([
            { id: 'context' as RightTab, icon: MessageSquare },
            { id: 'provider' as RightTab, icon: Radio },
            { id: 'im' as RightTab, icon: Bot },
            { id: 'settings' as RightTab, icon: Sliders },
          ]).map(t => (
            <button key={t.id} className={`right-tab ${rightTab === t.id ? 'active' : ''}`} onClick={() => setRightTab(t.id)}>
              <t.icon size={14} />
            </button>
          ))}
        </div>

        <div className="right-tab-content">
          {rightTab === 'context' && (
            <>
              <div className="context-section"><h4>Token Usage</h4>
                <div className="token-stats"><span>In: {totalInputTokens.toLocaleString()}</span><span>Out: {totalOutputTokens.toLocaleString()}</span></div>
              </div>
              <div className="context-section"><h4>Messages</h4>
                <div className="file-item" style={{ color: 'var(--text-muted)' }}>{messages.length} messages</div>
              </div>
            </>
          )}

          {rightTab === 'provider' && (
            <>
              <div className="context-section"><h4>Vendor</h4>
                <select className="config-select" value={activeProvider.vendor} onChange={e => {
                  const v = vendors.find(v => v.name === e.target.value);
                  const ep = v?.endpoints[0];
                  handleProviderChange(e.target.value, ep?.name || '', ep?.selectedModel || ep?.models?.[0] || '');
                }}>
                  {vendors.map(v => <option key={v.name} value={v.name}>{v.displayName}</option>)}
                </select>
              </div>
              {currentVendor && <div className="context-section"><h4>Endpoint</h4>
                <select className="config-select" value={activeProvider.endpoint} onChange={e => {
                  const ep = currentVendor.endpoints.find(ep => ep.name === e.target.value);
                  handleProviderChange(activeProvider.vendor, e.target.value, ep?.selectedModel || ep?.models?.[0] || '');
                }}>
                  {currentVendor.endpoints.map(ep => <option key={ep.name} value={ep.name}>{ep.displayName || ep.name}</option>)}
                </select>
              </div>}
              {currentEndpoint && <div className="context-section"><h4>Model</h4>
                <select className="config-select" value={activeProvider.model} onChange={e => handleProviderChange(activeProvider.vendor, activeProvider.endpoint, e.target.value)}>
                  {currentEndpoint.models.map(m => <option key={m} value={m}>{m}</option>)}
                </select>
              </div>}
              <div className="context-section"><h4>API Key</h4>
                <input className="config-input" type="password" placeholder="Enter API key..." onBlur={async e => {
                  if (e.target.value.trim()) { await ChatService.SetEndpointAPIKey(activeProvider.vendor, activeProvider.endpoint, e.target.value.trim()); e.target.value = ''; }
                }} />
              </div>
            </>
          )}

          {rightTab === 'im' && (
            <>
              <div className="context-section"><h4>IM Adapters</h4>
                {imAdapters.length === 0 ? <div className="file-item" style={{ color: 'var(--text-muted)' }}>No adapters configured</div> :
                  imAdapters.map(a => (
                    <div key={a.name} className="im-adapter-item">
                      <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                        <input type="checkbox" checked={a.enabled} onChange={async () => {
                          await ChatService.SetIMAdapterEnabled(a.name, !a.enabled);
                          setIMAdapters((await ChatService.GetIMAdapters()) as any);
                        }} />
                        <span style={{ fontWeight: 500 }}>{a.name}</span>
                        <span style={{ fontSize: 11, color: 'var(--text-muted)' }}>{a.platform}</span>
                      </div>
                      <button className="remove-btn" onClick={async () => {
                        await ChatService.RemoveIMAdapter(a.name);
                        setIMAdapters((await ChatService.GetIMAdapters()) as any);
                      }}><X size={12} /></button>
                    </div>
                  ))}
              </div>
            </>
          )}

          {rightTab === 'settings' && (
            <>
              <div className="context-section"><h4>Theme</h4>
                <select className="config-select" value={theme} onChange={e => setTheme(e.target.value)}>
                  <option value="system">System</option><option value="dark">Dark</option><option value="light">Light</option>
                </select>
              </div>
              <div className="context-section"><h4>Font Size</h4>
                <div style={{ display: 'flex', alignItems: 'center', gap: 8 }}>
                  <input type="range" min={12} max={20} value={fontSize} onChange={e => setFontSize(parseInt(e.target.value))} style={{ flex: 1 }} />
                  <span style={{ fontSize: 12, color: 'var(--text-muted)' }}>{fontSize}px</span>
                </div>
              </div>
              <div className="context-section"><h4>Language</h4>
                <select className="config-select" value={settings?.language || 'en'} onChange={async e => {
                  await ChatService.SetLanguage(e.target.value);
                  setSettings(await ChatService.GetSettings());
                }}>
                  <option value="en">English</option><option value="zh-CN">中文</option>
                </select>
              </div>
              <div className="context-section"><h4>Max Iterations</h4>
                <input className="config-input" type="number" value={settings?.maxIterations || 10} onBlur={async e => {
                  await ChatService.SetMaxIterations(parseInt(e.target.value) || 10);
                  setSettings(await ChatService.GetSettings());
                }} />
              </div>
              <div className="context-section"><h4>Version</h4>
                <div className="file-item">v{settings?.version || 'dev'}</div>
                <button className="config-btn" style={{ marginTop: 6 }} onClick={async () => setUpdateInfo(await ChatService.CheckForUpdates())}>
                  <RefreshCw size={12} style={{ display: 'inline', marginRight: 4 }} />Check for Updates
                </button>
                {updateInfo && <div style={{ marginTop: 6, fontSize: 12, color: updateInfo.error ? 'var(--error)' : 'var(--text-secondary)' }}>
                  {updateInfo.error || (updateInfo.hasUpdate ? `Update: ${updateInfo.latestVersion}` : 'Up to date')}
                </div>}
              </div>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

export default App;
