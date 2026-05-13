import { type CSSProperties, useEffect, useRef, useState } from 'react'
import type { Mapping } from '../types/api'
import { searchMappings } from '../api/client'
import { colors, radius, shadow } from '../theme'

// On-demand mapping search — a read-only lookup over every stored mapping in
// the org. Lives in the top nav and is always visible. Operators type a
// substring (any of CPN, IPN, MPN, or description) and get matching mappings
// across all client buckets.
//
// Result rows lead with the *internal* part number because that's the answer
// to Andrew's stated questions ("have I seen this part?", "what are the
// current mappings for this part?") — the value he'll paste into SAP next.
// Each P/N value is independently click-to-copy so operators can grab whichever
// field answers the current question.
//
// Keyboard navigation: ↑/↓ moves the highlight, Enter copies the highlighted
// row's IPN, Esc closes.
export default function MappingSearch() {
  const [query,    setQuery]    = useState('')
  const [results,  setResults]  = useState<Mapping[]>([])
  const [loading,  setLoading]  = useState(false)
  const [open,     setOpen]     = useState(false)
  const [error,    setError]    = useState<string | null>(null)
  const [selected, setSelected] = useState(0) // highlighted row index for keyboard nav

  const inputRef = useRef<HTMLInputElement>(null)
  const wrapRef  = useRef<HTMLDivElement>(null)
  // Token counter for stale-response guard. Each fetch reads the counter at
  // dispatch; on resolve we ignore the response if the counter has moved on.
  const fetchToken = useRef(0)

  // Debounce + stale-response guard.
  useEffect(() => {
    const q = query.trim()
    setSelected(0)
    if (q === '') {
      setResults([])
      setError(null)
      setLoading(false)
      return
    }
    setLoading(true)
    const myToken = ++fetchToken.current
    const handle = setTimeout(() => {
      searchMappings(q, { limit: 20 })
        .then(rs => {
          if (myToken !== fetchToken.current) return // a newer query has fired; drop
          setResults(rs)
          setError(null)
        })
        .catch(e => {
          if (myToken !== fetchToken.current) return
          setResults([])
          setError((e as Error).message)
        })
        .finally(() => {
          if (myToken !== fetchToken.current) return
          setLoading(false)
        })
    }, 180)
    return () => clearTimeout(handle)
  }, [query])

  // Close the dropdown on outside click.
  useEffect(() => {
    function onClick(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onClick)
    return () => document.removeEventListener('mousedown', onClick)
  }, [])

  // Keep highlighted index inside the results array as it changes.
  useEffect(() => {
    if (selected >= results.length) setSelected(Math.max(0, results.length - 1))
  }, [results, selected])

  async function copyValue(value: string) {
    try {
      await navigator.clipboard.writeText(value)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = value
      document.body.appendChild(ta)
      ta.select()
      try { document.execCommand('copy') } catch { /* best-effort */ }
      document.body.removeChild(ta)
    }
  }

  function handleKeyDown(e: React.KeyboardEvent<HTMLInputElement>) {
    if (e.key === 'Escape') {
      setOpen(false)
      ;(e.target as HTMLInputElement).blur()
      return
    }
    if (!open || results.length === 0) return
    if (e.key === 'ArrowDown') {
      e.preventDefault()
      setSelected(i => Math.min(results.length - 1, i + 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setSelected(i => Math.max(0, i - 1))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const r = results[selected]
      if (r) {
        // Default action mirrors Andrew's typical workflow: copy the IPN.
        // Fall back to MPN, then CPN, if IPN is empty.
        const target = r.internalPartNumber || r.manufacturerPartNumber || r.customerPartNumber
        if (target) copyValue(target)
      }
    }
  }

  function clearAndRefocus() {
    setQuery('')
    setResults([])
    setError(null)
    setSelected(0)
    inputRef.current?.focus()
  }

  const showDropdown = open && (query.trim() !== '' || loading)
  const showEmptyHint = open && query.trim() === '' && !loading

  return (
    <div ref={wrapRef} style={wrap}>
      <SearchIcon />
      <input
        ref={inputRef}
        value={query}
        onChange={e => { setQuery(e.target.value); setOpen(true) }}
        onFocus={() => setOpen(true)}
        onKeyDown={handleKeyDown}
        placeholder="Search part mappings…"
        style={input}
        aria-label="Search part mappings"
        aria-autocomplete="list"
        aria-controls="mapping-search-results"
        aria-activedescendant={results[selected] ? `mapping-search-row-${results[selected].id}` : undefined}
      />
      {query && (
        <button
          onClick={clearAndRefocus}
          style={clearBtn}
          aria-label="Clear search"
          tabIndex={-1}
        >×</button>
      )}
      {showEmptyHint && (
        <div style={dropdown}>
          <div style={dropdownMeta}>Type to search across part numbers and descriptions. ↑↓ to navigate, Enter to copy.</div>
        </div>
      )}
      {showDropdown && (
        <div style={dropdown} id="mapping-search-results" role="listbox">
          {loading && <div style={dropdownMeta}>Searching…</div>}
          {!loading && error && <div style={dropdownError}>{error}</div>}
          {!loading && !error && results.length === 0 && (
            <div style={dropdownMeta}>No mappings match “{query.trim()}”.</div>
          )}
          {!loading && !error && results.length > 0 && (
            <>
              <div style={dropdownHeader}>{results.length} {results.length === 1 ? 'mapping' : 'mappings'}</div>
              {results.map((m, i) => (
                <ResultRow
                  key={m.id}
                  mapping={m}
                  highlighted={i === selected}
                  onHover={() => setSelected(i)}
                />
              ))}
            </>
          )}
        </div>
      )}
    </div>
  )
}

function ResultRow({
  mapping, highlighted, onHover,
}: { mapping: Mapping; highlighted: boolean; onHover: () => void }) {
  // Computed inline — useMemo here is misleading because Date.now() inside
  // the function means the value would invalidate every render anyway.
  const lastUsed = formatRelativeTime(mapping.lastUsedAt)
  return (
    <div
      style={highlighted ? { ...resultRow, background: '#f0effe' } : resultRow}
      onMouseEnter={onHover}
      id={`mapping-search-row-${mapping.id}`}
      role="option"
      aria-selected={highlighted}
    >
      {/* Row 1: client badge + the IPN as the headline answer.
          Andrew's "have I seen this part? what's the mapping?" question is
          answered by the internal P/N — that's what goes into SAP. CPN
          and MPN are secondary context, demoted to the footer. */}
      <div style={resultHeader}>
        <span style={clientBadge(mapping.clientLabel)}>
          {mapping.clientLabel || '(generic)'}
        </span>
        {mapping.internalPartNumber ? (
          <CopyableValue value={mapping.internalPartNumber} label="internal P/N" emphasis />
        ) : (
          <span style={{ fontSize: 12, fontStyle: 'italic', color: colors.textSubtle }}>
            no internal P/N
          </span>
        )}
      </div>
      {mapping.description && (
        <div style={resultDesc}>{mapping.description}</div>
      )}
      <div style={resultFooter}>
        <span style={fieldGroup}>
          <span style={fieldLabel}>Customer</span>
          <CopyableValue value={mapping.customerPartNumber} label="customer P/N" />
        </span>
        {mapping.manufacturerPartNumber && (
          <span style={fieldGroup}>
            <span style={fieldLabel}>Mfr</span>
            <CopyableValue value={mapping.manufacturerPartNumber} label="manufacturer P/N" />
          </span>
        )}
        <span style={metaTail}>
          {mapping.source} · used {lastUsed}
        </span>
      </div>
    </div>
  )
}

// CopyableValue renders a P/N value as a click-to-copy chip with a clear
// hover state. Each P/N field in a result row is independently copyable so
// operators can grab whichever field answers their current question.
function CopyableValue({ value, label, emphasis = false }: { value: string; label: string; emphasis?: boolean }) {
  const [copied, setCopied] = useState(false)

  async function copy(e: React.MouseEvent) {
    e.stopPropagation()
    try {
      await navigator.clipboard.writeText(value)
      setCopied(true)
      setTimeout(() => setCopied(false), 1100)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = value
      document.body.appendChild(ta)
      ta.select()
      try { document.execCommand('copy'); setCopied(true); setTimeout(() => setCopied(false), 1100) } catch { /* best-effort */ }
      document.body.removeChild(ta)
    }
  }

  const className = [
    'copyable',
    emphasis ? 'copyable--emphasis' : '',
    copied ? 'copyable--copied' : '',
  ].filter(Boolean).join(' ')

  return (
    <button
      type="button"
      onClick={copy}
      title={copied ? 'Copied!' : `Click to copy ${label}`}
      className={className}
    >
      <span style={{ fontFamily: 'monospace' }}>{value}</span>
      {copied ? (
        <span style={{ fontSize: 10, fontWeight: 600 }}>✓ copied</span>
      ) : (
        <ClipboardIcon />
      )}
    </button>
  )
}

function ClipboardIcon() {
  return (
    <svg
      className="copyable-icon"
      width="11" height="11" viewBox="0 0 16 16" fill="none" aria-hidden="true"
    >
      <rect x="4" y="3" width="8" height="11" rx="1.3" stroke="currentColor" strokeWidth="1.4" />
      <path d="M6 3V2.5a1 1 0 011-1h2a1 1 0 011 1V3" stroke="currentColor" strokeWidth="1.4" strokeLinecap="round" />
    </svg>
  )
}

function SearchIcon() {
  return (
    <svg width="14" height="14" viewBox="0 0 16 16" fill="none" aria-hidden="true"
      style={{ position: 'absolute', left: 10, top: '50%', transform: 'translateY(-50%)', color: colors.textMuted, pointerEvents: 'none' }}>
      <circle cx="7" cy="7" r="5" stroke="currentColor" strokeWidth="1.5" />
      <path d="M11 11l3 3" stroke="currentColor" strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  )
}

function formatRelativeTime(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const diff = Date.now() - t
  const s = Math.floor(diff / 1000)
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60); if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60); if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24); if (d < 30) return `${d}d ago`
  const mo = Math.floor(d / 30); if (mo < 12) return `${mo}mo ago`
  return `${Math.floor(mo / 12)}y ago`
}

// ── styles ────────────────────────────────────────────────────────────────────

const wrap: CSSProperties = {
  position: 'relative', flex: 1, maxWidth: 480, margin: '0 24px',
}

const input: CSSProperties = {
  width: '100%', padding: '7px 30px 7px 32px',
  fontSize: 13,
  border: `1px solid ${colors.border}`, borderRadius: radius.md,
  background: colors.bg, color: colors.text,
  outline: 'none',
}

const clearBtn: CSSProperties = {
  position: 'absolute', right: 8, top: '50%', transform: 'translateY(-50%)',
  background: 'none', border: 'none', cursor: 'pointer',
  color: colors.textSubtle, fontSize: 16, lineHeight: 1, padding: '0 4px',
}

const dropdown: CSSProperties = {
  position: 'absolute', top: 'calc(100% + 4px)', left: 0, right: 0,
  background: colors.surface, border: `1px solid ${colors.border}`,
  borderRadius: radius.md, boxShadow: shadow.lg,
  maxHeight: 440, overflowY: 'auto', zIndex: 250,
}

const dropdownHeader: CSSProperties = {
  padding: '6px 12px', fontSize: 11, fontWeight: 600,
  color: colors.textMuted, textTransform: 'uppercase', letterSpacing: '0.05em',
  borderBottom: `1px solid ${colors.borderLight}`, background: colors.bg,
}

const dropdownMeta: CSSProperties = {
  padding: '12px', fontSize: 12, color: colors.textMuted, textAlign: 'center',
}

const dropdownError: CSSProperties = {
  ...dropdownMeta, color: colors.errorText,
}

const resultRow: CSSProperties = {
  padding: '8px 12px',
  borderBottom: `1px solid ${colors.borderLight}`,
  cursor: 'default',
}

const resultHeader: CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 8, marginBottom: 3, flexWrap: 'wrap',
}

const resultDesc: CSSProperties = {
  fontSize: 12, color: colors.text, marginBottom: 4,
  overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
}

const resultFooter: CSSProperties = {
  display: 'flex', alignItems: 'center', gap: 10, flexWrap: 'wrap',
  fontSize: 11, color: colors.textMuted,
}

const fieldGroup: CSSProperties = {
  display: 'inline-flex', alignItems: 'center', gap: 4,
}

const fieldLabel: CSSProperties = {
  fontSize: 10, color: colors.textSubtle, textTransform: 'uppercase', letterSpacing: '0.04em',
}

const metaTail: CSSProperties = {
  fontSize: 11, color: colors.textSubtle, marginLeft: 'auto',
}

function clientBadge(label: string): CSSProperties {
  const hasLabel = label !== ''
  return {
    fontSize: 11, fontWeight: 600,
    padding: '1px 7px', borderRadius: radius.full,
    background: hasLabel ? '#e0f2fe' : '#f3f4f6',
    color: hasLabel ? '#075985' : colors.textSubtle,
    maxWidth: 200, overflow: 'hidden', textOverflow: 'ellipsis', whiteSpace: 'nowrap',
  }
}
