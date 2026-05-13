import React, { useState } from 'react';
import ReactMarkdown from 'react-markdown';
import remarkGfm from 'remark-gfm';
import { Prism as SyntaxHighlighter } from 'react-syntax-highlighter';
import { oneDark } from 'react-syntax-highlighter/dist/esm/styles/prism';
import {
  ChevronRight,
  ChevronDown,
  Wrench,
  AlertTriangle,
  Brain,
  Copy,
  Check,
  FileText,
  Terminal,
  Search,
} from 'lucide-react';
import type { ContentBlock } from '../types';
import { useChatStore } from '../store';

// ─── Main message renderer ─────────────────────────────────────

interface MessageBubbleProps {
  role: 'user' | 'assistant' | 'system';
  content: ContentBlock[];
  messageIndex: number;
}

export const MessageBubble: React.FC<MessageBubbleProps> = ({
  role,
  content,
  messageIndex,
}) => {
  const isUser = role === 'user';

  return (
    <div className={`message-bubble ${isUser ? 'message-user' : 'message-assistant'}`}>
      <div className="message-avatar">
        {isUser ? (
          <div className="avatar avatar-user">U</div>
        ) : (
          <div className="avatar avatar-assistant">G</div>
        )}
      </div>
      <div className="message-body">
        {content.map((block, i) => (
          <ContentBlockRenderer
            key={`${messageIndex}-${i}`}
            block={block}
            messageIndex={messageIndex}
          />
        ))}
      </div>
    </div>
  );
};

// ─── Content block renderer (dispatches by type) ────────────────

interface BlockProps {
  block: ContentBlock;
  messageIndex: number;
}

const ContentBlockRenderer: React.FC<BlockProps> = ({ block, messageIndex }) => {
  switch (block.type) {
    case 'text':
      return <TextBlock text={block.text || ''} />;
    case 'tool_use':
      return <ToolUseBlock block={block} />;
    case 'tool_result':
      return <ToolResultBlock block={block} />;
    case 'reasoning':
      return <ReasoningBlock block={block} messageIndex={messageIndex} />;
    default:
      return null;
  }
};

// ─── Text block with full Markdown + code highlighting ──────────

const TextBlock: React.FC<{ text: string }> = ({ text }) => {
  if (!text) return null;

  return (
    <div className="content-text">
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          code({ className, children, ...props }) {
            const match = /language-(\w+)/.exec(className || '');
            const codeString = String(children).replace(/\n$/, '');

            if (!match) {
              return (
                <code className="inline-code" {...props}>
                  {children}
                </code>
              );
            }

            return <CodeBlock language={match[1]} code={codeString} />;
          },
          a({ href, children }) {
            return (
              <a href={href} onClick={(e) => e.preventDefault()} className="markdown-link">
                {children}
              </a>
            );
          },
          table({ children }) {
            return (
              <div className="table-wrapper">
                <table>{children}</table>
              </div>
            );
          },
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
};

// ─── Code block with copy button and language label ─────────────

const CodeBlock: React.FC<{ language: string; code: string }> = ({ language, code }) => {
  const [copied, setCopied] = useState(false);

  const handleCopy = () => {
    navigator.clipboard.writeText(code);
    setCopied(true);
    setTimeout(() => setCopied(false), 2000);
  };

  return (
    <div className="code-block">
      <div className="code-header">
        <span className="code-language">{language}</span>
        <button className="code-copy-btn" onClick={handleCopy} title="Copy code">
          {copied ? <Check size={14} /> : <Copy size={14} />}
          {copied ? 'Copied!' : 'Copy'}
        </button>
      </div>
      <SyntaxHighlighter
        language={language}
        style={oneDark}
        customStyle={{
          margin: 0,
          borderRadius: '0 0 8px 8px',
          fontSize: '13px',
          lineHeight: '1.5',
        }}
      >
        {code}
      </SyntaxHighlighter>
    </div>
  );
};

// ─── Collapsible tool use block ─────────────────────────────────

const ToolUseBlock: React.FC<{ block: ContentBlock }> = ({ block }) => {
  const collapsed = useChatStore((s) => s.collapsedTools[block.tool_id || '']);
  const toggle = useChatStore((s) => s.toggleToolCollapsed);
  const isCollapsed = collapsed !== false; // default collapsed

  const icon = getToolIcon(block.tool_name || '');
  const inputPreview = getInputPreview(block);

  return (
    <div className={`tool-block tool-use ${isCollapsed ? 'collapsed' : 'expanded'}`}>
      <div className="tool-header" onClick={() => toggle(block.tool_id || '')}>
        <div className="tool-header-left">
          {isCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
          {icon}
          <span className="tool-name">{block.tool_name || 'tool'}</span>
          {isCollapsed && <span className="tool-preview">{inputPreview}</span>}
        </div>
        <span className="tool-badge">calling</span>
      </div>
      {!isCollapsed && block.input && (
        <div className="tool-content">
          <pre className="tool-json">{JSON.stringify(block.input, null, 2)}</pre>
        </div>
      )}
    </div>
  );
};

// ─── Collapsible tool result block ──────────────────────────────

const ToolResultBlock: React.FC<{ block: ContentBlock }> = ({ block }) => {
  const collapsed = useChatStore((s) => s.collapsedTools[block.tool_id || '']);
  const toggle = useChatStore((s) => s.toggleToolCollapsed);
  const isCollapsed = collapsed !== false;

  const outputPreview = getOutputPreview(block.output || '');
  const icon = block.is_error ? (
    <AlertTriangle size={14} className="icon-error" />
  ) : (
    <Check size={14} className="icon-success" />
  );

  return (
    <div
      className={`tool-block tool-result ${
        block.is_error ? 'tool-error' : 'tool-success'
      } ${isCollapsed ? 'collapsed' : 'expanded'}`}
    >
      <div className="tool-header" onClick={() => toggle(block.tool_id || '')}>
        <div className="tool-header-left">
          {isCollapsed ? <ChevronRight size={14} /> : <ChevronDown size={14} />}
          {icon}
          <span className="tool-name">{block.tool_name || 'result'}</span>
          {isCollapsed && <span className="tool-preview">{outputPreview}</span>}
        </div>
        <span className={`tool-badge ${block.is_error ? 'badge-error' : 'badge-success'}`}>
          {block.is_error ? 'error' : 'done'}
        </span>
      </div>
      {!isCollapsed && (
        <div className="tool-content">
          {block.output && (
            <pre className={`tool-output ${block.is_error ? 'output-error' : ''}`}>
              {block.output.length > 10000
                ? block.output.slice(0, 10000) + '\n... (truncated)'
                : block.output}
            </pre>
          )}
          {block.images && block.images.length > 0 && (
            <div className="tool-images">
              {block.images.map((img, i) => (
                <img
                  key={i}
                  src={`data:${img.mime};base64,${img.base64}`}
                  alt={`tool output ${i + 1}`}
                  className="tool-image"
                />
              ))}
            </div>
          )}
        </div>
      )}
    </div>
  );
};

// ─── Collapsible reasoning/thinking block ───────────────────────

const ReasoningBlock: React.FC<{
  block: ContentBlock;
  messageIndex: number;
}> = ({ block, messageIndex }) => {
  const visible = useChatStore((s) => s.reasoningVisible[messageIndex]);
  const toggle = useChatStore((s) => s.toggleReasoning);
  const isVisible = visible === true;

  if (!block.text) return null;

  return (
    <div className={`reasoning-block ${isVisible ? 'expanded' : 'collapsed'}`}>
      <div className="reasoning-header" onClick={() => toggle(messageIndex)}>
        <Brain size={14} className="icon-reasoning" />
        <span className="reasoning-label">Thinking</span>
        {isVisible ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
      </div>
      {isVisible && (
        <div className="reasoning-content">
          <ReactMarkdown remarkPlugins={[remarkGfm]}>{block.text}</ReactMarkdown>
        </div>
      )}
    </div>
  );
};

// ─── Helpers ────────────────────────────────────────────────────

function getToolIcon(name: string): React.ReactNode {
  const n = name.toLowerCase();
  if (n.includes('file') || n.includes('read') || n.includes('write'))
    return <FileText size={14} />;
  if (n.includes('bash') || n.includes('command') || n.includes('exec'))
    return <Terminal size={14} />;
  if (n.includes('grep') || n.includes('search') || n.includes('glob'))
    return <Search size={14} />;
  return <Wrench size={14} />;
}

function getInputPreview(block: ContentBlock): string {
  if (!block.input) return '';
  const json = JSON.stringify(block.input);
  if (json.length > 80) return json.slice(0, 77) + '...';
  return json;
}

function getOutputPreview(output: string): string {
  if (!output) return '(empty)';
  const firstLine = output.split('\n')[0];
  if (firstLine.length > 80) return firstLine.slice(0, 77) + '...';
  return firstLine;
}
