import { useEffect, useRef, useState } from 'react'
import { Navigate, Route, Routes, useLocation, useNavigate } from 'react-router-dom'
import type { BOMRow, Document, DocumentStatus, ExportConfig, Mapping } from './types/api'
import {
  analyzeDocument, checkAuth, exportCSVUrl, exportSAPUrl, exportTSVUrl,
  getExportConfig, login, logout, saveBOM, saveMapping, uploadDocument,
} from './api/client'
import AdminPage from './components/AdminPage'
import BomTable from './components/BomTable'
import InvitePage from './components/InvitePage'
import { LogoWordmark } from './components/Logo'
import LoginPage from './components/LoginPage'
import SettingsPage from './components/SettingsPage'
import UploadArea from './components/UploadArea'
import WarningsPanel from './components/WarningsPanel'
import { colors, font, radius, shadow } from './theme'

// ── Per-document state ───────────────────────────────────────────────────────

interface DocEntry {
  doc:       Document
  rows:      BOMRow[]
  uploading: boolean   // file being transferred to server
  saved:     boolean
  error:     string | null
}

// ── Concurrency semaphore ────────────────────────────────────────────────────

function createSemaphore(limit: number) {
  let active = 0
  const queue: Array<() => void> = []
  return {
    async acquire() {
      if (active < limit) { active++; return }
      await new Promise<void>(resolve => queue.push(resolve))
      active++
    },
    release() {
      active--
      const next = queue.shift()
      if (next) next()
    },
  }
}

const ANALYSIS_CONCURRENCY = 3

// ── App ──────────────────────────────────────────────────────────────────────

export default function App() {
  const navigate = useNavigate()
  const location = useLocation()
  const [authed,       setAuthed]       = useState<boolean | null>(null)
  const [isAdmin,      setIsAdmin]      = useState(false)
  const [entries,      setEntries]      = useState<Map<string, DocEntry>>(new Map())
  const [activeId,     setActiveId]     = useState<string | null>(null)
  const [copied,       setCopied]       = useState(false)
  const [exportConfig, setExportConfig] = useState<ExportConfig>({ columns: ['internalPartNumber', 'empty', 'quantity'], includeHeader: false })
  const sem = useRef(createSemaphore(ANALYSIS_CONCURRENCY))

  useEffect(() => {
    checkAuth().then(({ ok, isAdmin }) => {
      setAuthed(ok)
      setIsAdmin(isAdmin)
      if (ok) getExportConfig().then(setExportConfig).catch(() => {})
    })
  }, [])

  // Functional update helper — safe to call from any async context.
  function patchEntry(id: string, patch: Partial<DocEntry>) {
    setEntries(prev => {
      const e = prev.get(id)
      if (!e) return prev
      const next = new Map(prev)
      next.set(id, { ...e, ...patch })
      return next
    })
  }

  // ── Auth ──────────────────────────────────────────────────────────────────

  async function handleLogin(username: string, password: string) {
    await login(username, password)
    setAuthed(true)
    checkAuth().then(({ isAdmin }) => setIsAdmin(isAdmin)).catch(() => {})
  }

  async function handleLogout() {
    await logout()
    setAuthed(false)
  }

  // ── Upload + auto-analyze ─────────────────────────────────────────────────

  async function handleUpload(files: File[]) {
    // 1. Add placeholder entries immediately so the queue renders right away.
    const placeholders = files.map(file => {
      const tempId = `uploading-${Date.now()}-${Math.random()}`
      const placeholder: DocEntry = {
        doc: {
          id:         tempId,
          filename:   file.name,
          status:     'uploaded',
          uploadedAt: new Date().toISOString(),
          bomRows:    [],
          warnings:   [],
        },
        rows:      [],
        uploading: true,
        saved:     false,
        error:     null,
      }
      return { tempId, file, placeholder }
    })

    setEntries(prev => {
      const next = new Map(prev)
      for (const { tempId, placeholder } of placeholders) {
        next.set(tempId, placeholder)
      }
      return next
    })
    // Select first new doc so user sees something immediately.
    setActiveId(placeholders[0].tempId)

    // 2. Upload each file; swap placeholder for real doc on success.
    const uploadedDocs: Document[] = []
    await Promise.all(placeholders.map(async ({ tempId, file }) => {
      try {
        const doc = await uploadDocument(file)
        setEntries(prev => {
          const next = new Map(prev)
          next.delete(tempId)
          next.set(doc.id, { doc, rows: [], uploading: false, saved: false, error: null })
          return next
        })
        // Keep the active selection pointing at the real doc.
        setActiveId(prev => prev === tempId ? doc.id : prev)
        uploadedDocs.push(doc)
      } catch (e) {
        patchEntry(tempId, { uploading: false, error: (e as Error).message })
      }
    }))

    // 3. Auto-analyze all successful uploads, bounded by semaphore.
    await Promise.all(uploadedDocs.map(doc => runAnalysis(doc.id)))
  }

  async function runAnalysis(id: string) {
    patchEntry(id, { error: null })
    // Signal "analyzing" via doc.status — update the doc object.
    setEntries(prev => {
      const e = prev.get(id)
      if (!e) return prev
      const next = new Map(prev)
      next.set(id, { ...e, doc: { ...e.doc, status: 'analyzing' } })
      return next
    })

    await sem.current.acquire()
    try {
      const result = await analyzeDocument(id)
      patchEntry(id, { doc: result, rows: result.bomRows, saved: false })
    } catch (e) {
      patchEntry(id, {
        doc: { ...(entries.get(id)?.doc ?? {} as Document), status: 'error' },
        error: (e as Error).message,
      })
    } finally {
      sem.current.release()
    }
  }

  // ── Per-doc actions ───────────────────────────────────────────────────────

  async function handleSave(id: string, rows: BOMRow[]) {
    try {
      await saveBOM(id, rows)
      patchEntry(id, { saved: true })
    } catch (e) {
      patchEntry(id, { error: (e as Error).message })
    }
  }

  async function handleSaveMapping(
    mapping: Pick<Mapping, 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber' | 'description' | 'source'>,
  ) {
    await saveMapping(mapping)
  }

  function handleRowsChange(id: string, rows: BOMRow[]) {
    patchEntry(id, { rows, saved: false })
  }

  function handleRemove(id: string) {
    setEntries(prev => {
      const next = new Map(prev)
      next.delete(id)
      return next
    })
    setActiveId(prev => {
      if (prev !== id) return prev
      // Pick the first remaining entry as the new active, or null.
      const remaining = [...entries.keys()].filter(k => k !== id)
      return remaining[0] ?? null
    })
  }

  function exportColumnValue(row: BOMRow, col: string): string {
    switch (col) {
      case 'lineNumber':             return String(row.lineNumber)
      case 'description':            return row.description
      case 'quantity':               return row.quantity.value != null ? String(row.quantity.value) : row.quantity.raw
      case 'unit':                   return row.quantity.unit ?? ''
      case 'customerPartNumber':     return row.customerPartNumber
      case 'internalPartNumber':     return row.internalPartNumber
      case 'manufacturerPartNumber': return row.manufacturerPartNumber
      case 'notes':                  return row.notes
      case 'empty':                  return ''
      default:                       return ''
    }
  }

  function handleCopyForSAP(rows: BOMRow[]) {
    const allLines: string[] = []
    if (exportConfig.includeHeader) {
      const headerLabels: Record<string, string> = {
        lineNumber: 'Line', description: 'Description', quantity: 'Quantity',
        unit: 'Unit', customerPartNumber: 'Customer Part Number',
        internalPartNumber: 'Internal Part Number',
        manufacturerPartNumber: 'Manufacturer Part Number', notes: 'Notes',
      }
      allLines.push(exportConfig.columns.map(c => headerLabels[c] ?? c).join('\t'))
    }
    rows
      .filter(r => r.internalPartNumber.trim() !== '')
      .forEach(r => allLines.push(exportConfig.columns.map(c => exportColumnValue(r, c)).join('\t')))
    navigator.clipboard.writeText(allLines.join('\n')).then(() => {
      setCopied(true)
      setTimeout(() => setCopied(false), 2000)
    })
  }

  // ── Render ────────────────────────────────────────────────────────────────

  if (authed === null) return null

  // Public route: invite signup — accessible without auth
  if (location.pathname.startsWith('/invite/')) {
    return (
      <Routes>
        <Route path="/invite/:token" element={
          <InvitePage onAccepted={() => { setAuthed(true); navigate('/') }} />
        } />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    )
  }

  if (!authed) return <LoginPage onLogin={handleLogin} />

  const activeEntry = activeId ? (entries.get(activeId) ?? null) : null
  const hasEntries  = entries.size > 0
  const hasResults  = activeEntry?.doc.status === 'done' && (activeEntry.rows.length > 0)

  return (
    <div style={{ fontFamily: font.body, minHeight: '100vh', background: colors.bg, color: colors.text }}>

      {/* ── Sticky nav ──────────────────────────────────────────────────────── */}
      <header style={navHeader}>
        <div style={navInner}>
          <button style={{ background: 'none', border: 'none', padding: 0, cursor: 'pointer' }} onClick={() => navigate('/')} aria-label="Go to home">
            <LogoWordmark size={28} />
          </button>
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            {isAdmin && (
              <button style={ghostBtn} onClick={() => navigate('/admin')} title="Admin">Admin</button>
            )}
            <button style={iconBtn} onClick={() => navigate('/settings')} title="Settings" aria-label="Settings">
              <SlidersIcon />
            </button>
            <button style={ghostBtn} onClick={handleLogout}>Sign out</button>
          </div>
        </div>
      </header>

      {/* ── Routed content ──────────────────────────────────────────────────── */}
      <Routes>
        <Route path="/settings" element={<SettingsPage />} />
        {isAdmin && <Route path="/admin" element={<AdminPage />} />}
        <Route path="/" element={
          <main style={mainStyle}>

            <div style={{ marginBottom: 24 }}>
              <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
                Drawing to BOM
              </h1>
              <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
                Upload customer drawing PDFs to generate draft Bills of Materials for review.
              </p>
            </div>

            {/* Upload area — full when empty, compact strip when docs are present */}
            {hasEntries ? (
              <UploadArea onUpload={handleUpload} loading={false} compact />
            ) : (
              <UploadArea onUpload={handleUpload} loading={false} />
            )}

            {/* Document queue */}
            {hasEntries && (
              <div style={queueGrid}>
                {[...entries.entries()].map(([id, entry]) => (
                  <DocCard
                    key={id}
                    entry={entry}
                    active={id === activeId}
                    onClick={() => setActiveId(id)}
                    onRemove={() => handleRemove(id)}
                    onRetry={() => runAnalysis(id)}
                  />
                ))}
              </div>
            )}

            {/* Active document detail */}
            {activeEntry && (
              <>
                {activeEntry.error && (
                  <div style={errorBanner}>
                    <strong style={{ fontWeight: 600 }}>Error:</strong> {activeEntry.error}
                  </div>
                )}

                <div style={docBar}>
                  <div style={{ display: 'flex', alignItems: 'center', gap: 10, minWidth: 0 }}>
                    <span style={{ fontWeight: 600, fontSize: 15, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}>
                      {activeEntry.doc.filename}
                    </span>
                    <StatusBadge status={activeEntry.doc.status} uploading={activeEntry.uploading} />
                  </div>
                  <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center', flexShrink: 0 }}>
                    {activeEntry.doc.status === 'error' && (
                      <button style={primaryBtn} onClick={() => runAnalysis(activeEntry.doc.id)}>
                        Retry Analysis
                      </button>
                    )}
                    {hasResults && (
                      <>
                        <button
                          style={activeEntry.saved ? savedBtn : secondaryBtn}
                          onClick={() => handleSave(activeEntry.doc.id, activeEntry.rows)}
                        >
                          {activeEntry.saved ? 'Saved ✓' : 'Save Changes'}
                        </button>
                        <button style={copied ? savedBtn : secondaryBtn} onClick={() => handleCopyForSAP(activeEntry.rows)}>
                          {copied ? 'Copied ✓' : 'Copy for SAP'}
                        </button>
                        <a href={exportSAPUrl(activeEntry.doc.id)} style={secondaryBtn} download>Export SAP</a>
                        <a href={exportTSVUrl(activeEntry.doc.id)} style={secondaryBtn} download>Export TSV</a>
                        <a href={exportCSVUrl(activeEntry.doc.id)} style={secondaryBtn} download>Export CSV</a>
                      </>
                    )}
                  </div>
                </div>

                <WarningsPanel warnings={activeEntry.doc.warnings ?? []} />

                {hasResults ? (
                  <BomTable
                    rows={activeEntry.rows}
                    onChange={rows => handleRowsChange(activeEntry.doc.id, rows)}
                    onSaveMapping={handleSaveMapping}
                  />
                ) : activeEntry.uploading ? (
                  <EmptyState><span className="spinner" style={{ borderTopColor: colors.brand, borderColor: colors.border }} />Uploading…</EmptyState>
                ) : activeEntry.doc.status === 'uploaded' ? (
                  <EmptyState>Ready to analyze.</EmptyState>
                ) : activeEntry.doc.status === 'analyzing' ? (
                  <EmptyState>Analyzing drawing…</EmptyState>
                ) : activeEntry.doc.status === 'error' ? (
                  <EmptyState>Analysis failed. See error above.</EmptyState>
                ) : null}
              </>
            )}

          </main>
        } />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </div>
  )
}

// ── DocCard ───────────────────────────────────────────────────────────────────

function DocCard({
  entry, active, onClick, onRemove, onRetry,
}: {
  entry:    DocEntry
  active:   boolean
  onClick:  () => void
  onRemove: () => void
  onRetry:  () => void
}) {
  const { doc, rows, uploading, error } = entry
  const busy = uploading || doc.status === 'analyzing'

  return (
    <div
      onClick={onClick}
      style={{
        position:     'relative',
        padding:      '12px 14px',
        background:   colors.surface,
        border:       `1.5px solid ${active ? colors.brand : colors.border}`,
        borderRadius: radius.lg,
        cursor:       'pointer',
        boxShadow:    active ? `0 0 0 3px ${colors.brandLight}` : shadow.sm,
        transition:   'border-color 0.15s, box-shadow 0.15s',
        minWidth:     0,
      }}
    >
      {/* Remove button — only when not busy */}
      {!busy && (
        <button
          onClick={e => { e.stopPropagation(); onRemove() }}
          style={cardRemoveBtn}
          title="Remove"
        >×</button>
      )}

      {/* Filename */}
      <div style={{ fontSize: 13, fontWeight: 600, color: colors.text, paddingRight: 18,
        overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 6 }}>
        {doc.filename}
      </div>

      {/* Status row */}
      <div style={{ display: 'flex', alignItems: 'center', gap: 6, flexWrap: 'wrap' }}>
        <StatusBadge status={doc.status} uploading={uploading} />
        {doc.status === 'done' && (
          <span style={{ fontSize: 11, color: colors.textMuted }}>
            {rows.length} {rows.length === 1 ? 'item' : 'items'}
          </span>
        )}
        {busy && <span className="spinner" style={{ borderTopColor: colors.brand, borderColor: colors.borderLight, width: 10, height: 10 }} />}
      </div>

      {/* Error snippet */}
      {error && (
        <div style={{ marginTop: 6, fontSize: 11, color: colors.errorText,
          overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap' }}
          title={error}
        >
          {error}
        </div>
      )}
    </div>
  )
}

// ── Shared sub-components ─────────────────────────────────────────────────────

function SlidersIcon() {
  return (
    <svg width="15" height="15" viewBox="0 0 15 15" fill="none" aria-hidden="true">
      <path d="M2 3.5h11M2 7.5h11M2 11.5h11" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
      <circle cx="9.5"  cy="3.5"  r="1.8" fill={colors.surface} stroke="currentColor" strokeWidth="1.4" />
      <circle cx="5"    cy="7.5"  r="1.8" fill={colors.surface} stroke="currentColor" strokeWidth="1.4" />
      <circle cx="10.5" cy="11.5" r="1.8" fill={colors.surface} stroke="currentColor" strokeWidth="1.4" />
    </svg>
  )
}

function StatusBadge({ status, uploading }: { status: DocumentStatus; uploading?: boolean }) {
  if (uploading) {
    return (
      <span style={{ padding: '3px 10px', borderRadius: radius.full, fontSize: 12, fontWeight: 600,
        background: colors.brandLight, color: colors.brandDark }}>
        uploading
      </span>
    )
  }
  const map: Record<DocumentStatus, { bg: string; color: string }> = {
    uploaded:  { bg: colors.brandLight,  color: colors.brandDark   },
    analyzing: { bg: colors.warningBg,   color: colors.warningText },
    done:      { bg: colors.successBg,   color: colors.successText },
    error:     { bg: colors.errorBg,     color: colors.errorText   },
  }
  const s = map[status]
  return (
    <span style={{ padding: '3px 10px', borderRadius: radius.full, fontSize: 12, fontWeight: 600,
      background: s.bg, color: s.color }}>
      {status}
    </span>
  )
}

function EmptyState({ children }: { children: React.ReactNode }) {
  return (
    <div style={{ padding: 56, textAlign: 'center', color: colors.textMuted,
      border: `1.5px dashed ${colors.border}`, borderRadius: radius.lg,
      fontSize: 14, lineHeight: 1.6, background: colors.surface }}>
      {children}
    </div>
  )
}

// ── Styles ────────────────────────────────────────────────────────────────────

const navHeader: React.CSSProperties = {
  position: 'sticky', top: 0, zIndex: 100,
  background: colors.surface, boxShadow: shadow.header,
}

const navInner: React.CSSProperties = {
  maxWidth: 1200, margin: '0 auto', padding: '0 24px', height: 58,
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
}

const mainStyle: React.CSSProperties = {
  maxWidth: 1200, margin: '0 auto', padding: '36px 24px 72px',
}

const queueGrid: React.CSSProperties = {
  display:             'grid',
  gridTemplateColumns: 'repeat(auto-fill, minmax(200px, 1fr))',
  gap:                 10,
  margin:              '16px 0',
}

const errorBanner: React.CSSProperties = {
  background: colors.errorBg, color: colors.errorText,
  border: `1px solid ${colors.errorBorder}`, padding: '12px 16px',
  borderRadius: radius.md, fontSize: 14, lineHeight: 1.5, marginBottom: 16,
}

const docBar: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
  flexWrap: 'wrap', gap: 12, padding: '12px 16px',
  background: colors.surface, border: `1px solid ${colors.border}`,
  borderRadius: radius.lg, marginBottom: 16, boxShadow: shadow.sm,
}

const primaryBtn: React.CSSProperties = {
  padding: '8px 18px', background: colors.brand, color: '#fff',
  border: 'none', borderRadius: radius.md, cursor: 'pointer',
  fontSize: 14, fontWeight: 600, fontFamily: font.body,
}

const secondaryBtn: React.CSSProperties = {
  padding: '8px 16px', background: colors.surface, color: colors.text,
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  cursor: 'pointer', fontSize: 14, fontWeight: 500,
  textDecoration: 'none', display: 'inline-block', fontFamily: font.body,
}

const savedBtn: React.CSSProperties = {
  ...secondaryBtn,
  background: colors.successBg, color: colors.successText, borderColor: colors.successBorder,
}

const ghostBtn: React.CSSProperties = {
  padding: '7px 14px', background: 'transparent', color: colors.textMuted,
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  cursor: 'pointer', fontSize: 14, fontFamily: font.body,
}

const iconBtn: React.CSSProperties = {
  width: 34, height: 34, display: 'flex', alignItems: 'center', justifyContent: 'center',
  background: 'transparent', color: colors.textMuted, border: `1px solid ${colors.border}`,
  borderRadius: radius.md, cursor: 'pointer', padding: 0, flexShrink: 0,
}

const cardRemoveBtn: React.CSSProperties = {
  position: 'absolute', top: 6, right: 6,
  width: 18, height: 18, padding: 0, lineHeight: 1,
  background: 'none', border: 'none', cursor: 'pointer',
  color: colors.textSubtle, fontSize: 16, display: 'flex',
  alignItems: 'center', justifyContent: 'center',
}
