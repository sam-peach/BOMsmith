import { useEffect, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { getAdminErrors } from '../api/client'
import type { ErrorLogEntry } from '../types/api'
import { colors, font, radius, shadow } from '../theme'

export default function AdminPage() {
  const navigate = useNavigate()
  const [entries,  setEntries]  = useState<ErrorLogEntry[]>([])
  const [loading,  setLoading]  = useState(true)
  const [error,    setError]    = useState<string | null>(null)

  useEffect(() => {
    getAdminErrors()
      .then(setEntries)
      .catch(e => setError((e as Error).message))
      .finally(() => setLoading(false))
  }, [])

  return (
    <main style={mainStyle}>
      <div style={{ marginBottom: 28 }}>
        <button style={backBtn} onClick={() => navigate('/')}>← Back</button>
        <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
          Admin — Error Log
        </h1>
        <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
          Recent errors and warnings from the analysis pipeline.
        </p>
      </div>

      <section style={card}>
        {loading && <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>Loading…</p>}
        {error   && <p style={{ margin: 0, color: colors.errorText,  fontSize: 14 }}>{error}</p>}

        {!loading && !error && entries.length === 0 && (
          <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>No errors recorded.</p>
        )}

        {!loading && !error && entries.length > 0 && (
          <div style={{ overflowX: 'auto' }}>
            <table style={table}>
              <thead>
                <tr>
                  <th style={th}>Time</th>
                  <th style={th}>Level</th>
                  <th style={th}>Component</th>
                  <th style={th}>Document</th>
                  <th style={{ ...th, width: '100%' }}>Message</th>
                </tr>
              </thead>
              <tbody>
                {entries.map((e, i) => (
                  <tr key={i} style={{ background: i % 2 === 0 ? 'transparent' : colors.bg }}>
                    <td style={td}>{new Date(e.timestamp).toLocaleString()}</td>
                    <td style={td}>
                      <span style={levelBadge(e.level)}>{e.level}</span>
                    </td>
                    <td style={td}>{e.component}</td>
                    <td style={{ ...td, color: colors.textMuted }}>{e.docName ?? ''}</td>
                    <td style={{ ...td, fontFamily: 'monospace', fontSize: 12, wordBreak: 'break-all' }}>{e.message}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </section>
    </main>
  )
}

function levelBadge(level: string): React.CSSProperties {
  const isError = level === 'error'
  return {
    display:      'inline-block',
    padding:      '2px 8px',
    borderRadius: radius.full,
    fontSize:     11,
    fontWeight:   600,
    background:   isError ? colors.errorBg  : colors.warningBg,
    color:        isError ? colors.errorText : colors.warningText,
  }
}

// ── Styles ───────────────────────────────────────────────────────────────────

const mainStyle: React.CSSProperties = {
  maxWidth: 900,
  margin:   '0 auto',
  padding:  '36px 24px 72px',
}

const card: React.CSSProperties = {
  background:   colors.surface,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.lg,
  padding:      '24px 28px',
  boxShadow:    shadow.sm,
}

const backBtn: React.CSSProperties = {
  display:      'inline-block',
  marginBottom: 12,
  padding:      '6px 0',
  background:   'none',
  border:       'none',
  color:        colors.textMuted,
  cursor:       'pointer',
  fontSize:     13,
  fontFamily:   font.body,
}

const table: React.CSSProperties = {
  width:           '100%',
  borderCollapse:  'collapse',
  fontSize:        13,
  tableLayout:     'auto',
}

const th: React.CSSProperties = {
  textAlign:    'left',
  padding:      '8px 12px',
  fontWeight:   600,
  fontSize:     12,
  color:        colors.textMuted,
  borderBottom: `1px solid ${colors.border}`,
  whiteSpace:   'nowrap',
}

const td: React.CSSProperties = {
  padding:      '8px 12px',
  verticalAlign: 'top',
  borderBottom: `1px solid ${colors.border}`,
  whiteSpace:   'nowrap',
}
