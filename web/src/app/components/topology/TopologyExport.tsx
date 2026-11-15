import { useState } from 'react'
import { Check, ClipboardCopy, Download, FileText } from 'lucide-react'

export function TopologyExport({ text }: { text: string }) {
  const [open, setOpen] = useState(false)
  const [copied, setCopied] = useState(false)

  const copy = async () => {
    try {
      await navigator.clipboard.writeText(text)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    } catch {
      // Fallback for non-secure contexts
      const ta = document.createElement('textarea')
      ta.value = text
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      document.body.removeChild(ta)
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1800)
    }
  }

  const download = () => {
    const blob = new Blob([text], { type: 'text/plain;charset=utf-8' })
    const url = URL.createObjectURL(blob)
    const a = document.createElement('a')
    a.href = url
    a.download = `warden-topology-${new Date().toISOString().slice(0, 10)}.txt`
    document.body.appendChild(a)
    a.click()
    document.body.removeChild(a)
    URL.revokeObjectURL(url)
  }

  return (
    <div className="relative">
      <button className="secondary-button" onClick={() => setOpen((v) => !v)} aria-expanded={open}>
        <FileText size={16} />
        导出拓扑
      </button>
      {open && (
        <>
          <button className="fixed inset-0 z-10 border-0 bg-transparent" aria-label="关闭导出" onClick={() => setOpen(false)} />
          <div className="absolute right-0 z-20 mt-2 w-[420px] max-w-[calc(100vw-2rem)] rounded-lg border border-white/70 bg-white/90 p-3 shadow-glass-strong backdrop-blur-2xl dark:border-white/15 dark:bg-surface-dark/90 dark:shadow-glass-dark-strong">
            <div className="mb-2 flex items-center justify-between">
              <p className="text-xs font-semibold text-content dark:text-content-dark">AI 可读拓扑文本</p>
              <div className="flex gap-1.5">
                <button className="quiet-button" onClick={copy}>
                  {copied ? (
                    <>
                      <Check size={14} />
                      已复制
                    </>
                  ) : (
                    <>
                      <ClipboardCopy size={14} />
                      复制
                    </>
                  )}
                </button>
                <button className="quiet-button" onClick={download}>
                  <Download size={14} />
                  下载
                </button>
              </div>
            </div>
            <pre className="max-h-[320px] overflow-auto rounded-md bg-surface-muted p-2.5 font-mono text-[10px] leading-relaxed text-content-subtle dark:bg-surface-dark-muted dark:text-content-dark-subtle">
              {text}
            </pre>
          </div>
        </>
      )}
    </div>
  )
}
