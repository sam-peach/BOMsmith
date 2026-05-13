import { useEffect, useState } from 'react'
import type { BOMPreview, BOMRow, MatchFeedback, SimilarDocument } from '../types/api'
import { cloneFromDocument, getBOMPreview, getSimilarDocuments, recordMatchFeedback } from '../api/client'
import { colors, font, radius, shadow } from '../theme'

interface Props {
  docId: string
  onClone: (result: { bomRows: BOMRow[]; warnings: string[] }) => void
}

export default function SimilarDrawings({ docId, onClone }: Props) {
  const [similar,   setSimilar]   = useState<SimilarDocument[] | null>(null) // null = loading
  const [cloning,   setCloning]   = useState<string | null>(null)
  const [dismissed, setDismissed] = useState(false)
  const [previews,  setPreviews]  = useState<Map<string, BOMPreview | 'loading' | 'error'>>(new Map())
  const [rejected,  setRejected]  = useState(false)
  const [expanded,  setExpanded]  = useState(false) // collapsed by default — the count alone tells the operator whether it's worth opening

  useEffect(() => {
    setSimilar(null)
    setDismissed(false)
    setRejected(false)
    setExpanded(false)
    setPreviews(new Map())
    getSimilarDocuments(docId).then(setSimilar).catch(() => setSimilar([]))
  }, [docId])

  if (similar === null || dismissed || rejected) return null

  async function handleClone(candidate: SimilarDocument) {
    setCloning(candidate.id)
    try {
      const doc = await cloneFromDocument(docId, candidate.id)
      // Record accept feedback (best-effort, don't block on it)
      const fb: MatchFeedback = {
        drawingId:      docId,
        candidateId:    candidate.id,
        action:         'accept',
        score:          candidate.score,
        scoreBreakdown: candidate.scoreBreakdown,
      }
      recordMatchFeedback([fb]).catch(() => {})
      onClone({ bomRows: doc.bomRows, warnings: doc.warnings })
    } catch (e) {
      console.error('Clone failed:', e)
    } finally {
      setCloning(null)
    }
  }

  async function handleRejectAll() {
    if (!similar || similar.length === 0) { setRejected(true); return }
    const items: MatchFeedback[] = similar.map(c => ({
      drawingId:      docId,
      candidateId:    c.id,
      action:         'reject',
      score:          c.score,
      scoreBreakdown: c.scoreBreakdown,
    }))
    recordMatchFeedback(items).catch(() => {})
    setRejected(true)
  }

  async function togglePreview(id: string) {
    if (previews.has(id)) {
      // Already loaded or loading — toggle off by removing.
      setPreviews(prev => { const next = new Map(prev); next.delete(id); return next })
      return
    }
    setPreviews(prev => new Map(prev).set(id, 'loading'))
    try {
      const preview = await getBOMPreview(id)
      setPreviews(prev => new Map(prev).set(id, preview))
    } catch {
      setPreviews(prev => new Map(prev).set(id, 'error'))
    }
  }

  if (similar.length === 0) {
    return (
      <div style={emptyPanelStyle}>
        <span style={{ color: colors.textMuted, fontSize: 13 }}>
          No strong matches found in past drawings.
        </span>
      </div>
    )
  }

  return (
    <div style={panelStyle}>
      <div style={headerRow}>
        <button
          style={titleBtn}
          onClick={() => setExpanded(e => !e)}
          aria-expanded={expanded}
          title={expanded ? 'Collapse' : `Show ${similar.length} similar past drawing${similar.length === 1 ? '' : 's'}`}
        >
          <span style={caret(expanded)}>▸</span>
          <span>Similar past drawings</span>
          <span style={countBadge}>{similar.length}</span>
          {!expanded && (
            <span style={hiddenHint}>
              {similar.length === 1 ? '1 possible match hidden' : `${similar.length} possible matches hidden`}
            </span>
          )}
        </button>
        <button style={dismissBtn} onClick={() => setDismissed(true)} aria-label="Dismiss">×</button>
      </div>
      {expanded && (
        <>
          <div style={hintRow}>
            <p style={panelHint}>
              These drawings share part numbers or filename with this one. Preview before reusing.
            </p>
            <button style={noneBtn} onClick={handleRejectAll} title="Dismiss all suggestions">
              None of these match
            </button>
          </div>
          <div style={listStyle}>
            {similar.map(doc => (
              <CandidateRow
                key={doc.id}
                doc={doc}
                preview={previews.get(doc.id) ?? null}
                cloning={cloning === doc.id}
                onPreview={() => togglePreview(doc.id)}
                onClone={() => handleClone(doc)}
              />
            ))}
          </div>
        </>
      )}
    </div>
  )
}

// ── CandidateRow ──────────────────────────────────────────────────────────────

function CandidateRow({
  doc, preview, cloning, onPreview, onClone,
}: {
  doc:       SimilarDocument
  preview:   BOMPreview | 'loading' | 'error' | null
  cloning:   boolean
  onPreview: () => void
  onClone:   () => void
}) {
  const pct  = Math.round(doc.score * 100)
  const date = new Date(doc.uploadedAt).toLocaleDateString(undefined, { dateStyle: 'medium' })
  const previewOpen = preview !== null

  return (
    <div style={rowWrapStyle}>
      <div style={rowStyle}>
        <div style={rowMain}>
          <div style={rowFilename} title={doc.filename}>{doc.filename}</div>
          <div style={rowMeta}>
            <span style={scoreBadge(pct)}>{pct}% match</span>
            <span style={metaText}>{doc.bomRowCount} items · {date}</span>
          </div>
          {doc.matchReasons.length > 0 && (
            <div style={reasonsStyle}>{doc.matchReasons.join(' · ')}</div>
          )}
        </div>
        <div style={actionsStyle}>
          <button
            style={previewOpen ? activePreviewBtn : previewBtn}
            onClick={onPreview}
            title={previewOpen ? 'Hide preview' : 'Preview BOM rows'}
          >
            {preview === 'loading' ? '…' : previewOpen ? 'Hide' : 'Preview'}
          </button>
          <button
            style={cloning ? cloningBtn : cloneBtn}
            onClick={onClone}
            disabled={cloning}
            title="Use this drawing's BOM as a starting point"
          >
            {cloning ? 'Applying…' : 'Use this BOM'}
          </button>
        </div>
      </div>

      {/* Inline expandable preview */}
      {previewOpen && preview !== 'loading' && preview !== 'error' && preview !== null && (
        <PreviewPanel preview={preview} />
      )}
      {preview === 'error' && (
        <div style={{ padding: '8px 12px', fontSize: 12, color: colors.errorText }}>
          Failed to load preview.
        </div>
      )}
    </div>
  )
}

function PreviewPanel({ preview }: { preview: BOMPreview }) {
  return (
    <div style={previewPanelStyle}>
      <div style={previewHeader}>
        <span style={{ fontWeight: 600, fontSize: 12 }}>{preview.filename}</span>
        {preview.totalRows > preview.rows.length && (
          <span style={{ fontSize: 11, color: colors.textMuted }}>
            Showing {preview.rows.length} of {preview.totalRows} rows
          </span>
        )}
      </div>
      <table style={previewTable}>
        <thead>
          <tr>
            {(['Description', 'Qty', 'Customer P/N', 'Mfr P/N'] as const).map(h => (
              <th key={h} style={thStyle}>{h}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {preview.rows.map((row, i) => (
            <tr key={row.id ?? i} style={i % 2 === 0 ? evenRow : oddRow}>
              <td style={tdStyle}>{row.description || '—'}</td>
              <td style={tdStyle}>{row.quantity.value ?? row.quantity.raw}</td>
              <td style={tdStyle}>{row.customerPartNumber || '—'}</td>
              <td style={tdStyle}>{row.manufacturerPartNumber || '—'}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}

// ── Styles ────────────────────────────────────────────────────────────────────

const panelStyle: React.CSSProperties = {
  background: colors.surface,
  border: `1px solid ${colors.border}`,
  borderRadius: radius.lg,
  padding: '14px 16px',
  marginBottom: 16,
  boxShadow: shadow.sm,
}

const emptyPanelStyle: React.CSSProperties = {
  ...panelStyle,
  padding: '12px 16px',
}

const headerRow: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between', marginBottom: 4,
}

const panelTitle: React.CSSProperties = {
  fontSize: 14, fontWeight: 600, color: colors.text,
  display: 'flex', alignItems: 'center', gap: 8,
}

const titleBtn: React.CSSProperties = {
  ...panelTitle,
  background: 'none', border: 'none', padding: 0,
  cursor: 'pointer', textAlign: 'left',
  fontFamily: font.body,
}

function caret(expanded: boolean): React.CSSProperties {
  return {
    display: 'inline-block', fontSize: 24, fontWeight: 700,
    color: colors.brand,
    width: 24, textAlign: 'center', lineHeight: 1,
    transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)',
    transition: 'transform 0.15s ease',
    flexShrink: 0,
  }
}

const hiddenHint: React.CSSProperties = {
  fontSize: 12, color: colors.textMuted, fontWeight: 400, marginLeft: 4,
}

const countBadge: React.CSSProperties = {
  fontSize: 11, fontWeight: 600,
  background: colors.brandLight, color: colors.brandDark,
  padding: '2px 7px', borderRadius: radius.full,
}

const panelHint: React.CSSProperties = {
  margin: 0, fontSize: 12, color: colors.textMuted, lineHeight: 1.4,
  textAlign: 'center',
}

const hintRow: React.CSSProperties = {
  display: 'flex', flexDirection: 'column', alignItems: 'center',
  gap: 8, marginBottom: 12,
}

const dismissBtn: React.CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  color: colors.textSubtle, fontSize: 18, lineHeight: 1,
  padding: '0 2px', fontFamily: font.body,
}

const noneBtn: React.CSSProperties = {
  background: 'none', border: `1px solid ${colors.border}`,
  borderRadius: radius.md, cursor: 'pointer',
  color: colors.textMuted, fontSize: 12, padding: '4px 10px',
  fontFamily: font.body,
}

const listStyle: React.CSSProperties = {
  display: 'flex', flexDirection: 'column', gap: 8,
}

const rowWrapStyle: React.CSSProperties = {
  border: `1px solid ${colors.border}`,
  borderRadius: radius.md,
  overflow: 'hidden',
}

const rowStyle: React.CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
  gap: 12, padding: '10px 12px',
  background: colors.bg,
}

const rowMain: React.CSSProperties = { minWidth: 0, flex: 1 }

const rowFilename: React.CSSProperties = {
  fontSize: 13, fontWeight: 600, color: colors.text,
  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap', marginBottom: 3,
}

const rowMeta: React.CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, marginBottom: 2,
}

const metaText: React.CSSProperties = { fontSize: 12, color: colors.textMuted }

const reasonsStyle: React.CSSProperties = {
  fontSize: 11, color: colors.textSubtle, marginTop: 2,
  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
}

const actionsStyle: React.CSSProperties = {
  display: 'flex', gap: 6, flexShrink: 0, alignItems: 'center',
}

function scoreBadge(pct: number): React.CSSProperties {
  const bg  = pct >= 70 ? colors.successBg  : pct >= 40 ? colors.warningBg  : colors.brandLight
  const col = pct >= 70 ? colors.successText : pct >= 40 ? colors.warningText : colors.brandDark
  return { fontSize: 11, fontWeight: 600, padding: '2px 7px', borderRadius: radius.full, background: bg, color: col }
}

const previewBtn: React.CSSProperties = {
  flexShrink: 0, padding: '6px 12px',
  background: colors.surface, color: colors.text,
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  cursor: 'pointer', fontSize: 12, fontFamily: font.body,
}

const activePreviewBtn: React.CSSProperties = {
  ...previewBtn,
  background: colors.brandLight, color: colors.brandDark, borderColor: colors.brand,
}

const cloneBtn: React.CSSProperties = {
  flexShrink: 0, padding: '6px 14px',
  background: colors.brand, color: '#fff',
  border: 'none', borderRadius: radius.md,
  cursor: 'pointer', fontSize: 13, fontWeight: 600, fontFamily: font.body,
}

const cloningBtn: React.CSSProperties = { ...cloneBtn, opacity: 0.6, cursor: 'not-allowed' }

const previewPanelStyle: React.CSSProperties = {
  borderTop: `1px solid ${colors.border}`,
  background: colors.surface,
  padding: '10px 12px',
}

const previewHeader: React.CSSProperties = {
  display: 'flex', justifyContent: 'space-between', alignItems: 'center',
  marginBottom: 8,
}

const previewTable: React.CSSProperties = {
  width: '100%', borderCollapse: 'collapse', fontSize: 12,
}

const thStyle: React.CSSProperties = {
  textAlign: 'left', padding: '4px 8px',
  color: colors.textMuted, fontWeight: 600, fontSize: 11,
  borderBottom: `1px solid ${colors.border}`,
}

const tdStyle: React.CSSProperties = {
  padding: '4px 8px', color: colors.text,
  maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
}

const evenRow: React.CSSProperties = { background: colors.surface }
const oddRow: React.CSSProperties  = { background: colors.bg }
