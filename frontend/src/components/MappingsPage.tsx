import { type CSSProperties, useEffect, useMemo, useState } from 'react'
import { useNavigate } from 'react-router-dom'
import { deleteMapping, listAllMappings, listMappingClients, saveMapping } from '../api/client'
import type { ClientMappingSummary, Mapping } from '../types/api'
import { colors, font, radius, shadow } from '../theme'

// MappingsPage is the maintenance surface for the org's cross-reference store.
// Operators can browse every stored mapping, filter by client / source / free
// text, and edit non-key fields in place. Renaming a mapping's primary key
// (client + customer P/N) is delete-and-recreate by design — keeps the surface
// honest about the underlying composite-key shape.
//
// Andrew asked for this in 2026-04-15: "some view of this might be useful to
// be able to upload current x-ref sheet and make corrections if errors are
// introduced". This is the "make corrections" half — the upload half is the
// Settings page client-mappings import.
export default function MappingsPage() {
  const navigate = useNavigate()
  const [mappings, setMappings] = useState<Mapping[]>([])
  const [clients,  setClients]  = useState<ClientMappingSummary[]>([])
  const [loading,  setLoading]  = useState(true)
  const [error,    setError]    = useState<string | null>(null)

  // Tracks rows that are mid-flight so the UI can disable destructive actions
  // (avoids double-fire on delete) and surface a per-row "Saved ✓" pill.
  const [savingId,  setSavingId]  = useState<string | null>(null)
  const [savedId,   setSavedId]   = useState<string | null>(null)
  const [deletingId, setDeletingId] = useState<string | null>(null)

  const [q,            setQ]            = useState('')
  // Filter sentinels: '__all__' = unfiltered, '__generic__' = empty-label bucket.
  // Using sentinels rather than the empty string disambiguates "All clients"
  // from "(generic)" — both would otherwise collapse to value="".
  const [clientFilter, setClientFilter] = useState<string>('__all__')
  const [sourceFilter, setSourceFilter] = useState<string>('') // '' = any

  async function reload() {
    setLoading(true)
    try {
      const [ms, cs] = await Promise.all([listAllMappings(), listMappingClients()])
      setMappings(ms)
      setClients(cs)
      setError(null)
    } catch (e) {
      setError((e as Error).message)
    } finally {
      setLoading(false)
    }
  }

  useEffect(() => { reload() }, [])

  const sources = useMemo(() => {
    const set = new Set(mappings.map(m => m.source))
    return Array.from(set).sort()
  }, [mappings])

  const filtered = useMemo(() => {
    const needle = q.trim().toLowerCase()
    return mappings.filter(m => {
      if (clientFilter === '__generic__') {
        if (m.clientLabel !== '') return false
      } else if (clientFilter !== '__all__') {
        if (m.clientLabel !== clientFilter) return false
      }
      if (sourceFilter !== '' && m.source !== sourceFilter) return false
      if (needle === '') return true
      return [m.customerPartNumber, m.internalPartNumber, m.manufacturerPartNumber, m.description]
        .some(v => v.toLowerCase().includes(needle))
    })
  }, [mappings, q, clientFilter, sourceFilter])

  async function handleEdit(m: Mapping, patch: Partial<Pick<Mapping, 'internalPartNumber' | 'manufacturerPartNumber' | 'description'>>) {
    // Optimistic update — flip back if the save fails so the UI doesn't lie.
    const original = m
    const next = { ...m, ...patch }
    setMappings(prev => prev.map(x => x.id === m.id ? next : x))
    setSavingId(m.id)
    try {
      await saveMapping({
        // clientLabel is the load-bearing field — without it the upsert lands
        // in the generic bucket and silently duplicates the row.
        clientLabel:            next.clientLabel,
        customerPartNumber:     next.customerPartNumber,
        internalPartNumber:     next.internalPartNumber,
        manufacturerPartNumber: next.manufacturerPartNumber,
        description:            next.description,
        source:                 next.source,
      })
      // Brief success pill — surfaces a positive confirmation that the
      // optimistic update was persisted, not just shown.
      setSavedId(m.id)
      setTimeout(() => setSavedId(curr => curr === m.id ? null : curr), 1500)
    } catch (e) {
      setMappings(prev => prev.map(x => x.id === m.id ? original : x))
      setError((e as Error).message)
    } finally {
      setSavingId(curr => curr === m.id ? null : curr)
    }
  }

  async function handleDelete(m: Mapping) {
    if (deletingId === m.id) return // guard against a re-entrant double-click
    const label = m.clientLabel ? `${m.clientLabel} · ${m.customerPartNumber}` : m.customerPartNumber
    if (!window.confirm(`Delete mapping for ${label}? This cannot be undone.`)) return
    const original = mappings
    setDeletingId(m.id)
    setMappings(prev => prev.filter(x => x.id !== m.id))
    try {
      await deleteMapping(m.id)
    } catch (e) {
      setMappings(original)
      setError((e as Error).message)
    } finally {
      setDeletingId(curr => curr === m.id ? null : curr)
    }
  }

  return (
    <main style={mainStyle}>
      <div style={{ marginBottom: 16 }}>
        <button style={backBtn} onClick={() => navigate('/')}>← Back</button>
        <h1 style={{ margin: '0 0 4px', fontSize: 20, fontWeight: 600, letterSpacing: '-0.02em' }}>
          Mappings
        </h1>
        <p style={{ margin: 0, color: colors.textMuted, fontSize: 14 }}>
          Browse, search, and edit every cross-reference mapping in the org.
        </p>
      </div>

      {error && (
        <div style={errorBanner}>
          {error}
          <button onClick={() => setError(null)} style={dismissBtn} aria-label="Dismiss">×</button>
        </div>
      )}

      <section style={{ ...card, marginBottom: 16 }}>
        <div style={filterRow}>
          <input
            value={q}
            onChange={e => setQ(e.target.value)}
            placeholder="Search CPN, IPN, MPN, or description…"
            style={searchInput}
          />
          <select value={clientFilter} onChange={e => setClientFilter(e.target.value)} style={select}>
            <option value="__all__">All clients</option>
            {clients.map(c => (
              <option key={c.label || '__generic__'} value={c.label === '' ? '__generic__' : c.label}>
                {c.label || '(generic)'} ({c.count})
              </option>
            ))}
          </select>
          <select value={sourceFilter} onChange={e => setSourceFilter(e.target.value)} style={select}>
            <option value="">All sources</option>
            {sources.map(s => <option key={s} value={s}>{s}</option>)}
          </select>
        </div>
        <div style={{ marginTop: 8, fontSize: 12, color: colors.textMuted }}>
          {loading ? 'Loading…' : `${filtered.length} of ${mappings.length} mappings`}
        </div>
      </section>

      <section style={{ ...card, padding: 0 }}>
        <div style={{ overflowX: 'auto' }}>
          <table style={table}>
            <thead>
              <tr>
                <th style={th}>Client</th>
                <th style={th}>Customer P/N</th>
                <th style={th}>Internal P/N</th>
                <th style={th}>Manufacturer P/N</th>
                <th style={th}>Description</th>
                <th style={th}>Source</th>
                <th style={{ ...th, textAlign: 'right' }}>Actions</th>
              </tr>
            </thead>
            <tbody>
              {filtered.map(m => (
                <MappingRow
                  key={m.id}
                  mapping={m}
                  saving={savingId === m.id}
                  saved={savedId === m.id}
                  deleting={deletingId === m.id}
                  onEdit={patch => handleEdit(m, patch)}
                  onDelete={() => handleDelete(m)}
                />
              ))}
              {!loading && filtered.length === 0 && (
                <tr>
                  <td colSpan={7} style={{ ...td, textAlign: 'center', color: colors.textMuted, padding: '24px 12px' }}>
                    {mappings.length === 0
                      ? 'No mappings yet. Import a client Excel from Settings, or save mappings from the BOM editor.'
                      : 'No mappings match the current filters.'}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </main>
  )
}

function MappingRow({
  mapping, saving, saved, deleting, onEdit, onDelete,
}: {
  mapping: Mapping
  saving: boolean
  saved: boolean
  deleting: boolean
  onEdit: (patch: Partial<Pick<Mapping, 'internalPartNumber' | 'manufacturerPartNumber' | 'description'>>) => void
  onDelete: () => void
}) {
  return (
    <tr>
      <td style={td}>
        <span style={clientBadge(mapping.clientLabel)}>
          {mapping.clientLabel || '(generic)'}
        </span>
      </td>
      <td style={tdMono} title="Primary key — delete and recreate to rename">{mapping.customerPartNumber}</td>
      <td style={td}>
        <EditableCell value={mapping.internalPartNumber} onSave={v => onEdit({ internalPartNumber: v })} />
      </td>
      <td style={td}>
        <EditableCell value={mapping.manufacturerPartNumber} onSave={v => onEdit({ manufacturerPartNumber: v })} />
      </td>
      <td style={td}>
        <EditableCell value={mapping.description} onSave={v => onEdit({ description: v })} wide />
      </td>
      <td style={{ ...td, fontSize: 11, color: colors.textMuted }}>{mapping.source}</td>
      <td style={{ ...td, textAlign: 'right', whiteSpace: 'nowrap' }}>
        {saving && <span style={statusPill('saving')}>Saving…</span>}
        {saved && !saving && <span style={statusPill('saved')}>Saved ✓</span>}
        <button
          onClick={onDelete}
          disabled={deleting}
          style={deleting ? { ...deleteBtn, opacity: 0.6, cursor: 'wait' } : deleteBtn}
          title={deleting ? 'Deleting…' : 'Delete mapping'}
        >
          {deleting ? 'Deleting…' : 'Delete'}
        </button>
      </td>
    </tr>
  )
}

// EditableCell renders a value that switches to an input on click; commits on
// blur or Enter; reverts on Escape. Mirrors the BomTable's edit-on-blur pattern
// so the gesture is consistent across the app.
function EditableCell({
  value, onSave, wide = false,
}: { value: string; onSave: (next: string) => void; wide?: boolean }) {
  const [editing, setEditing] = useState(false)
  const [draft,   setDraft]   = useState(value)

  useEffect(() => { setDraft(value) }, [value])

  function commit() {
    setEditing(false)
    if (draft !== value) onSave(draft.trim())
  }

  if (!editing) {
    return (
      <button
        onClick={() => setEditing(true)}
        style={editableButton(wide, value === '')}
        title="Click to edit"
      >
        {value || <span style={{ color: colors.textSubtle }}>—</span>}
      </button>
    )
  }
  return (
    <input
      value={draft}
      autoFocus
      onChange={e => setDraft(e.target.value)}
      onBlur={commit}
      onKeyDown={e => {
        if (e.key === 'Enter') commit()
        if (e.key === 'Escape') { setDraft(value); setEditing(false) }
      }}
      className="bom-input"
      style={{ width: wide ? 260 : 140 }}
    />
  )
}

// ── styles ────────────────────────────────────────────────────────────────────

const mainStyle: CSSProperties = {
  maxWidth: 1800, margin: '0 auto', padding: '36px 24px 72px',
}

const card: CSSProperties = {
  background: colors.surface,
  border: `1px solid ${colors.border}`,
  borderRadius: radius.lg,
  padding: 16,
  boxShadow: shadow.sm,
}

const filterRow: CSSProperties = {
  display: 'flex', gap: 8, flexWrap: 'wrap', alignItems: 'center',
}

const searchInput: CSSProperties = {
  flex: 1, minWidth: 220, maxWidth: 380,
  padding: '7px 10px',
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  fontSize: 13, fontFamily: font.body, background: colors.bg,
}

const select: CSSProperties = {
  padding: '7px 10px',
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  fontSize: 13, fontFamily: font.body, background: colors.bg,
  // Cap the rendered width so a long client label doesn't blow out the
  // filter row layout. The full label still shows on hover via the option text.
  maxWidth: 240, textOverflow: 'ellipsis', overflow: 'hidden',
}

const table: CSSProperties = {
  width: '100%', borderCollapse: 'collapse', fontSize: 13,
}

const th: CSSProperties = {
  textAlign: 'left',
  padding: '10px 12px',
  background: colors.bg,
  borderBottom: `2px solid ${colors.border}`,
  fontSize: 11, fontWeight: 600,
  color: colors.textMuted,
  textTransform: 'uppercase', letterSpacing: '0.05em',
}

const td: CSSProperties = {
  padding: '6px 12px',
  borderBottom: `1px solid ${colors.borderLight}`,
  verticalAlign: 'middle',
}

const tdMono: CSSProperties = {
  ...td, fontFamily: 'monospace', fontSize: 12, color: colors.text,
}

function editableButton(wide: boolean, empty: boolean): CSSProperties {
  return {
    display: 'inline-block',
    minWidth: wide ? 240 : 120,
    padding: '4px 8px',
    background: empty ? 'transparent' : colors.bg,
    border: `1px dashed ${empty ? colors.borderLight : 'transparent'}`,
    borderRadius: radius.sm,
    textAlign: 'left',
    fontFamily: 'monospace', fontSize: 12,
    color: colors.text,
    cursor: 'pointer',
  }
}

function clientBadge(label: string): CSSProperties {
  const hasLabel = label !== ''
  return {
    fontSize: 11, fontWeight: 600,
    padding: '2px 8px', borderRadius: radius.full,
    background: hasLabel ? '#e0f2fe' : '#f3f4f6',
    color: hasLabel ? '#075985' : colors.textSubtle,
    display: 'inline-block',
  }
}

const backBtn: CSSProperties = {
  display: 'inline-flex', alignItems: 'center',
  padding: '4px 10px', marginBottom: 12,
  background: 'transparent', border: `1px solid ${colors.border}`,
  borderRadius: radius.sm, fontSize: 12, color: colors.textMuted,
  cursor: 'pointer',
}

function statusPill(kind: 'saving' | 'saved'): CSSProperties {
  const bg = kind === 'saved' ? '#d1fae5' : '#fef3c7'
  const color = kind === 'saved' ? '#065f46' : '#92400e'
  return {
    display: 'inline-block', marginRight: 8,
    padding: '2px 8px', borderRadius: radius.full,
    fontSize: 11, fontWeight: 600, background: bg, color,
  }
}

const deleteBtn: CSSProperties = {
  padding: '4px 10px',
  background: 'transparent', border: `1px solid ${colors.border}`,
  borderRadius: radius.sm, fontSize: 12, color: colors.errorText,
  cursor: 'pointer',
}

const errorBanner: CSSProperties = {
  display: 'flex', alignItems: 'center', justifyContent: 'space-between',
  background: '#fee2e2', color: '#991b1b',
  padding: '10px 12px', borderRadius: radius.md, marginBottom: 12,
  fontSize: 13,
}

const dismissBtn: CSSProperties = {
  background: 'none', border: 'none', cursor: 'pointer',
  color: '#991b1b', fontSize: 18, lineHeight: 1, padding: '0 4px',
}
