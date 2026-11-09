import { useEffect, useRef, useState } from "react";
import { useChat } from "@/store/chat";
import MentionPopover from "@/chat/MentionPopover";

/** 附件条目:图片走 dataURL 预览+images 通道;其他文件/语音走 base64+files 通道 */
type PendingFile = { name: string; mime: string; data: string };

/** Composer(§4.3):发光活跃态+⌘⏎发送+Esc 中断面;@mention 弹层;📎附件+🎤语音 */
export default function Composer({
  onSend,
}: {
  onSend: (text: string, images?: string[], files?: PendingFile[]) => void;
}) {
  const [value, setValue] = useState("");
  const [focused, setFocused] = useState(false);
  const [pastedImages, setPastedImages] = useState<string[]>([]); // dataURL 缩略图预览(P4-4)
  const [attachFiles, setAttachFiles] = useState<PendingFile[]>([]);
  const [recording, setRecording] = useState(false);
  const busy = useChat((s) => s.busy);
  const conn = useChat((s) => s.conn);
  const taRef = useRef<HTMLTextAreaElement>(null);
  // 触屏设备(pointer: coarse):无组合键,Enter 直发(桌面保持 ⌘⏎ 发送/Shift+⏎ 换行)
  const isTouch = typeof matchMedia !== "undefined" && matchMedia("(pointer: coarse)").matches;
  const fileRef = useRef<HTMLInputElement>(null);
  const [mention, setMention] = useState<{ query: string; at: number; rect: DOMRect | null } | null>(null);
  const recorderRef = useRef<MediaRecorder | null>(null);
  const chunksRef = useRef<Blob[]>([]);

  // 自适应高度
  useEffect(() => {
    const ta = taRef.current;
    if (!ta) return;
    ta.style.height = "auto";
    ta.style.height = Math.min(ta.scrollHeight, 200) + "px";
  }, [value]);

  // 发光呼吸(仅 focus 且非 busy)
  useEffect(() => {
    const el = taRef.current?.closest("[data-composer]") as HTMLElement | null;
    if (!el) return;
    el.style.animation = focused && !busy ? "composer-breathe 2.4s ease-in-out infinite" : "";
    el.style.boxShadow = focused || busy ? "" : "var(--shadow-composer-idle)";
  }, [focused, busy]);

  const submit = () => {
    const text = value.trim();
    const hasImages = pastedImages.length > 0;
    const hasFiles = attachFiles.length > 0;
    // #5300: 断线不再禁发——sendUser 非 OPEN 入队、onopen 补发(client.ts §4.4)
    if ((!text && !hasImages && !hasFiles) || busy) return;
    onSend(text, hasImages ? pastedImages : undefined, hasFiles ? attachFiles : undefined);
    setValue("");
    setPastedImages([]);
    setAttachFiles([]);
  };

  // 截图/图片粘贴(P4-4):读为 dataURL 缩略图,发送时附 images
  const onPaste = (e: React.ClipboardEvent) => {
    const files = Array.from(e.clipboardData?.files ?? []).filter((f) => f.type.startsWith("image/"));
    if (files.length === 0) return;
    e.preventDefault();
    for (const f of files.slice(0, 4)) {
      const rd = new FileReader();
      rd.onload = () => setPastedImages((prev) => [...prev, String(rd.result)]);
      rd.readAsDataURL(f);
    }
  };

  // 📎 附件:图片归 images 通道(预览),其余归 files 通道(base64,后端包为文本块)
  const pickFiles = (list: FileList | null) => {
    if (!list) return;
    for (const f of Array.from(list).slice(0, 8)) {
      const rd = new FileReader();
      rd.onload = () => {
        const result = String(rd.result);
        if (f.type.startsWith("image/")) {
          setPastedImages((prev) => [...prev, result]);
        } else {
          const data = result.slice(result.indexOf(",") + 1);
          setAttachFiles((prev) => [...prev, { name: f.name, mime: f.type || "application/octet-stream", data }]);
        }
      };
      rd.readAsDataURL(f);
    }
  };

  // 🎤 语音:MediaRecorder 采集,停止后作为 voice-*.webm 附件发送
  const toggleRecord = async () => {
    if (recording) {
      recorderRef.current?.stop();
      return;
    }
    try {
      const stream = await navigator.mediaDevices.getUserMedia({ audio: true });
      const rec = new MediaRecorder(stream);
      chunksRef.current = [];
      rec.ondataavailable = (e) => chunksRef.current.push(e.data);
      rec.onstop = () => {
        stream.getTracks().forEach((t) => t.stop());
        setRecording(false);
        const blob = new Blob(chunksRef.current, { type: "audio/webm" });
        const rd = new FileReader();
        rd.onload = () => {
          const data = String(rd.result).slice(String(rd.result).indexOf(",") + 1);
          setAttachFiles((prev) => [...prev, { name: `voice-${Date.now()}.webm`, mime: "audio/webm", data }]);
        };
        rd.readAsDataURL(blob);
      };
      recorderRef.current = rec;
      rec.start();
      setRecording(true);
    } catch {
      /* 麦克风拒绝/不可用:静默(浏览器会弹权限提示) */
    }
  };

  return (
    <div className="border-t border-border bg-background px-6 py-4">
      <div
        data-composer
        className="mx-auto max-w-[860px] rounded-2xl border border-border bg-card p-3 transition-shadow"
      >
        <textarea
          ref={taRef}
          className="text-[16px] md:text-ui-base w-full resize-none bg-transparent text-wrap-phrase text-foreground placeholder:text-foreground-subtlest focus:outline-none"
          placeholder={isTouch ? "输入消息…(回车发送)" : "输入消息…(⌘⏎ 发送 / Shift+⏎ 换行)"}
          rows={2}
          value={value}
          onPaste={onPaste}
          onChange={(e) => {
            setValue(e.target.value);
            // @ 触发 mention 弹层(§4.3)
            const upto = e.target.value.slice(0, e.target.selectionStart ?? 0);
            const at = upto.lastIndexOf("@");
            if (at >= 0 && !upto.slice(at).includes(" ")) {
              setMention({ query: upto.slice(at + 1), at, rect: taRef.current?.getBoundingClientRect() ?? null });
            } else {
              setMention(null);
            }
          }}
          onFocus={() => setFocused(true)}
          onBlur={() => setFocused(false)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey && (isTouch || e.metaKey || e.ctrlKey)) {
              e.preventDefault();
              submit();
            }
          }}
        />
        {(pastedImages.length > 0 || attachFiles.length > 0) && (
          <div className="mb-2 flex flex-wrap gap-2">
            {pastedImages.map((src, i) => (
              <div key={`img-${i}`} className="relative">
                <img src={src} alt="paste" className="h-16 w-16 rounded-lg border border-border object-cover" />
                <button
                  className="absolute -right-1.5 -top-1.5 flex h-5 w-5 items-center justify-center rounded-full border border-border bg-popover text-ui-xs text-foreground-subtle"
                  onClick={() => setPastedImages((p) => p.filter((_, j) => j !== i))}
                  title="移除"
                >
                  ×
                </button>
              </div>
            ))}
            {attachFiles.map((f, i) => (
              <div key={`file-${i}`} className="text-ui-xs flex items-center gap-1 rounded-lg border border-border px-2 py-1 text-foreground-subtle">
                <span className="max-w-[160px] truncate">{f.name}</span>
                <button
                  className="text-foreground-subtlest hover:text-foreground"
                  onClick={() => setAttachFiles((p) => p.filter((_, j) => j !== i))}
                  title="移除"
                >
                  ×
                </button>
              </div>
            ))}
          </div>
        )}
        {mention && (
          <MentionPopover
            query={mention.query}
            anchorRect={mention.rect}
            onPick={(text) => {
              setMention(null);
              if (!text) return;
              // #5299: 替换触发性 @+query,而非末尾 append(曾必产 @@file: 畸形 token)
              const m = mention;
              setValue((v) => {
                if (!m) return v + text;
                return v.slice(0, m.at) + text + v.slice(m.at + 1 + m.query.length);
              });
              const pos = (m?.at ?? value.length) + text.length;
              taRef.current?.focus();
              requestAnimationFrame(() => taRef.current?.setSelectionRange(pos, pos));
            }}
          />
        )}
        <div className="flex items-center justify-between pt-1">
          <span className="text-ui-xs text-foreground-subtlest">
            {recording ? "🔴 录音中…" : conn !== "open" ? "离线中,消息将在重连后补发" : busy ? "生成中…(等待完成)" : isTouch ? "回车发送" : "⌘⏎ 发送"}
          </span>
          <div className="flex items-center gap-1.5">
            <input
              ref={fileRef}
              type="file"
              multiple
              className="hidden"
              onChange={(e) => {
                pickFiles(e.target.files);
                e.target.value = "";
              }}
            />
            <button
              className="text-ui-sm rounded-lg border border-border px-2 py-1 text-foreground-subtle transition-colors hover:text-foreground disabled:opacity-40"
              disabled={busy}
              title="附加文件"
              onClick={() => fileRef.current?.click()}
            >
              📎
            </button>
            <button
              className={`text-ui-sm rounded-lg border px-2 py-1 transition-colors disabled:opacity-40 ${
                recording ? "border-[var(--color-warning-surface-fg)] text-[var(--color-warning-surface-fg)]" : "border-border text-foreground-subtle hover:text-foreground"
              }`}
              disabled={busy}
              title={recording ? "停止录音并发送" : "语音输入(录音作为附件发送)"}
              onClick={toggleRecord}
            >
              🎤
            </button>
            <button
              className="text-ui-sm rounded-lg bg-brand px-4 py-1.5 font-medium text-white transition-opacity disabled:opacity-40"
              disabled={(!value.trim() && pastedImages.length === 0 && attachFiles.length === 0) || busy}
              onClick={submit}
            >
              发送
            </button>
          </div>
        </div>
      </div>
    </div>
  );
}
