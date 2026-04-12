import { useEffect, useState } from 'react'
import type { BOMRow, Document, DocumentStatus, Mapping } from './types/api'
import { analyzeDocument, checkAuth, exportCSVUrl, login, logout, saveBOM, saveMapping, uploadDocument } from './api/client'
import BomTable from './components/BomTable'
import { LogoWordmark } from './components/Logo'
import LoginPage from './components/LoginPage'
import SettingsPage from './components/SettingsPage'
import UploadArea from './components/UploadArea'
import WarningsPanel from './components/WarningsPanel'
import { colors, font, radius, shadow } from './theme'

export default function App() {
  const [authed,    setAuthed]    = useState<boolean | null>(null)
  const [view,      setView]      = useState<'main' | 'settings'>('main')
  const [doc,       setDoc]       = useState<Document | null>(null)
  const [rows,      setRows]      = useState<BOMRow[]>([])
  const [uploading, setUploading] = useState(false)
  const [analyzing, setAnalyzing] = useState(false)
  const [saved,     setSaved]     = useState(false)
  const [error,     setError]     = useState<string | null>(null)

  useEffect(() => {
    checkAuth().then(ok => setAuthed(ok))
  }, [])

  async function handleLogin(username: string, password: string) {
    await login(username, password)
    setAuthed(true)
  }

  async function handleLogout() {
    await logout()
    setAuthed(false)
  }

  async function handleUpload(file: File) {
    setError(null)
    setUploading(true)
    try {
      const uploaded = await uploadDocument(file)
      setDoc(uploaded)
      setRows([])
      setSaved(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setUploading(false)
    }
  }

  async function handleAnalyze() {
    if (!doc) return
    setError(null)
    setAnalyzing(true)
    try {
      const result = await analyzeDocument(doc.id)
      setDoc(result)
      setRows(result.bomRows)
      setSaved(false)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setAnalyzing(false)
    }
  }

  async function handleSave() {
    if (!doc) return
    setError(null)
    try {
      await saveBOM(doc.id, rows)
      setSaved(true)
    } catch (e) {
      setError((e as Error).message)
    }
  }

  async function handleSaveMapping(
    mapping: Pick<Mapping, 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber' | 'description' | 'source'>,
  ) {
    await saveMapping(mapping)
  }

  function handleRowsChange(next: BOMRow[]) {
    setRows(next)
    setSaved(false)
  }

  function handleReset() {
    setDoc(null)
    setRows([])
    setError(null)
    setSaved(false)
  }

  const hasResults = doc?.status === 'done' && rows.length > 0

  if (authed === null) return null
  if (!authed)             return <LoginPage onLogin={handleLogin} />
  if (view === 'settings') return <SettingsPage onBack={() => setView('main')} />

  return (
    <div style={{ fontFamily: font.body, minHeight: '100vh', background: colors.bg, color: colors.text }}>

      {/* ── Sticky nav ──────────────────────────────────────────────────────── */}
      <header style={navHeader}>
        <div style={navInner}>
          <LogoWordmark size={28} />
          <div style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
            <button
              style={iconBtn}
              onClick={() => setView('settings')}
              title="Settings"
              aria-label="Settings"
            >
              <SlidersIcon />
            </button>
            <button style={ghostBtn} onClick={handleLogout}>Sign out</button>
          </div>
        </div>
      </header>

      {/* ── Page content ────────────────────────────────────────────────────── */}
      <main style={mainStyle}>

        <div style={{ marginBottom: 28 }}>
          <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
            Drawing to BOM
          </h1>
          <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
            Upload a customer drawing PDF to generate a draft Bill of Materials for review.
          </p>
        </div>

        {error && (
          <div style={errorBanner}>
            <strong style={{ fontWeight: 600 }}>Error:</strong> {error}
          </div>
        )}

        {!doc ? (
          <UploadArea onUpload={handleUpload} loading={uploading} />
        ) : (
          <>
            {/* Document bar */}
            <div style={docBar}>
              <div style={{ display: 'flex', alignItems: 'center', gap: 10 }}>
                <span style={{ fontWeight: 600, fontSize: 15 }}>{doc.filename}</span>
                <StatusBadge status={doc.status} />
              </div>
              <div style={{ display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center' }}>
                {(doc.status === 'uploaded' || doc.status === 'error') && (
                  <button style={primaryBtn} onClick={handleAnalyze} disabled={analyzing}>
                    {analyzing
                      ? <><span className="spinner" />Analyzing...</>
                      : 'Analyze Drawing'}
                  </button>
                )}
                {hasResults && (
                  <>
                    <button
                      style={saved ? savedBtn : secondaryBtn}
                      onClick={handleSave}
                    >
                      {saved ? 'Saved ✓' : 'Save Changes'}
                    </button>
                    <a href={exportCSVUrl(doc.id)} style={secondaryBtn} download>
                      Export CSV
                    </a>
                  </>
                )}
                <button style={ghostBtn} onClick={handleReset}>
                  Upload New
                </button>
              </div>
            </div>

            <WarningsPanel warnings={doc.warnings} />

            {hasResults ? (
              <BomTable rows={rows} onChange={handleRowsChange} onSaveMapping={handleSaveMapping} />
            ) : doc.status === 'uploaded' ? (
              <EmptyState>
                Drawing uploaded. Click <strong>Analyze Drawing</strong> to extract the BOM.
              </EmptyState>
            ) : doc.status === 'analyzing' ? (
              <EmptyState>Analyzing drawing…</EmptyState>
            ) : doc.status === 'error' ? (
              <EmptyState>Analysis failed. See error above.</EmptyState>
            ) : null}
          </>
        )}
      </main>
    </div>
  )
}

// ── Sub-components ──────────────────────────────────────────────────────────

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

function StatusBadge({ status }: { status: DocumentStatus }) {
  const map: Record<DocumentStatus, { bg: string; color: string }> = {
    uploaded:  { bg: colors.brandLight,  color: colors.brandDark   },
    analyzing: { bg: colors.warningBg,   color: colors.warningText },
    done:      { bg: colors.successBg,   color: colors.successText },
    error:     { bg: colors.errorBg,     color: colors.errorText   },
  }
  const s = map[status]
  return (
    <span style={{
      padding:      '3px 10px',
      borderRadius: radius.full,
      fontSize:     12,
      fontWeight:   600,
      background:   s.bg,
      color:        s.color,
    }}>
      {status}
    </span>
  )
}

function EmptyState({ children }: { children: React.ReactNode }) {
  return (
    <div style={{
      padding:      56,
      textAlign:    'center',
      color:        colors.textMuted,
      border:       `1.5px dashed ${colors.border}`,
      borderRadius: radius.lg,
      fontSize:     14,
      lineHeight:   1.6,
      background:   colors.surface,
    }}>
      {children}
    </div>
  )
}

// ── Styles ──────────────────────────────────────────────────────────────────

const navHeader: React.CSSProperties = {
  position:   'sticky',
  top:        0,
  zIndex:     100,
  background: colors.surface,
  boxShadow:  shadow.header,
}

const navInner: React.CSSProperties = {
  maxWidth:       1200,
  margin:         '0 auto',
  padding:        '0 24px',
  height:         58,
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'space-between',
}

const mainStyle: React.CSSProperties = {
  maxWidth: 1200,
  margin:   '0 auto',
  padding:  '36px 24px 72px',
}

const errorBanner: React.CSSProperties = {
  background:   colors.errorBg,
  color:        colors.errorText,
  border:       `1px solid ${colors.errorBorder}`,
  padding:      '12px 16px',
  borderRadius: radius.md,
  fontSize:     14,
  lineHeight:   1.5,
  marginBottom: 20,
}

const docBar: React.CSSProperties = {
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'space-between',
  flexWrap:       'wrap',
  gap:            12,
  padding:        '12px 16px',
  background:     colors.surface,
  border:         `1px solid ${colors.border}`,
  borderRadius:   radius.lg,
  marginBottom:   16,
  boxShadow:      shadow.sm,
}

const primaryBtn: React.CSSProperties = {
  padding:      '8px 18px',
  background:   colors.brand,
  color:        '#fff',
  border:       'none',
  borderRadius: radius.md,
  cursor:       'pointer',
  fontSize:     14,
  fontWeight:   600,
  fontFamily:   font.body,
}

const secondaryBtn: React.CSSProperties = {
  padding:        '8px 16px',
  background:     colors.surface,
  color:          colors.text,
  border:         `1px solid ${colors.border}`,
  borderRadius:   radius.md,
  cursor:         'pointer',
  fontSize:       14,
  fontWeight:     500,
  textDecoration: 'none',
  display:        'inline-block',
  fontFamily:     font.body,
}

const savedBtn: React.CSSProperties = {
  ...secondaryBtn,
  background:   colors.successBg,
  color:        colors.successText,
  borderColor:  colors.successBorder,
}

const ghostBtn: React.CSSProperties = {
  padding:      '7px 14px',
  background:   'transparent',
  color:        colors.textMuted,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
  cursor:       'pointer',
  fontSize:     14,
  fontFamily:   font.body,
}

const iconBtn: React.CSSProperties = {
  width:          34,
  height:         34,
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'center',
  background:     'transparent',
  color:          colors.textMuted,
  border:         `1px solid ${colors.border}`,
  borderRadius:   radius.md,
  cursor:         'pointer',
  padding:        0,
  flexShrink:     0,
}
