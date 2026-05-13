import { type CSSProperties, useEffect, useRef, useState } from 'react'
import type { BOMRow, ConfirmableField, Mapping, Quantity, RowPricing } from '../types/api'
import { CONFIRMABLE_FIELDS, FLAG_PRICING_UNAVAILABLE } from '../types/api'
import { suggestMappings } from '../api/client'
import { colors, radius } from '../theme'

interface Props {
  rows: BOMRow[]
  onChange: (rows: BOMRow[]) => void
  onSaveMapping: (mapping: Pick<Mapping, 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber' | 'description' | 'source'>) => Promise<void>
}

// A cross-reference cell is Suggested when it has a value the system filled
// in (LLM extraction or catalog match) that the operator has not confirmed.
// Defensive against legacy rows where confirmedFields may be null/undefined.
function isSuggested(row: BOMRow, field: ConfirmableField): boolean {
  return row[field] !== '' && !(row.confirmedFields ?? []).includes(field)
}

function countSuggestedCells(rows: BOMRow[]): number {
  return rows.reduce(
    (n, r) => n + CONFIRMABLE_FIELDS.filter(f => isSuggested(r, f)).length,
    0,
  )
}

function confirmField(row: BOMRow, field: ConfirmableField): BOMRow {
  const current = row.confirmedFields ?? []
  if (current.includes(field)) return row
  return { ...row, confirmedFields: [...current, field] }
}

function confirmAllSuggestions(rows: BOMRow[]): BOMRow[] {
  return rows.map(r => {
    const toAdd = CONFIRMABLE_FIELDS.filter(f => isSuggested(r, f))
    if (toAdd.length === 0) return r
    return { ...r, confirmedFields: [...(r.confirmedFields ?? []), ...toAdd] }
  })
}

const COLUMNS = [
  { key: 'lineNumber',             label: '#',            width: 36  },
  { key: 'rawLabel',               label: 'Raw Label',    width: 100 },
  { key: 'description',            label: 'Description',  width: 220 },
  { key: 'quantity.raw',           label: 'Raw Qty',      width: 80  },
  { key: 'quantity.value',         label: 'Qty',          width: 64  },
  { key: 'quantity.unit',          label: 'Unit',         width: 54  },
  { key: 'customerPartNumber',     label: 'Cust. P/N',    width: 100 },
  { key: 'internalPartNumber',     label: 'Internal P/N', width: 110 },
  { key: 'manufacturerPartNumber', label: 'Mfr. P/N',     width: 150 },
  { key: 'supplierReference',      label: 'Supplier Ref', width: 110 },
  { key: 'notes',                  label: 'Notes',        width: 180 },
  { key: 'confidence',             label: 'Conf.',        width: 56  },
  { key: 'pricing',                label: 'Best Price',   width: 110 },
  { key: 'flags',                  label: 'Flags',        width: 160 },
  { key: '_actions',               label: '',             width: 60  },
]

// Standard quantity ladder for the details panel. We expose the four most
// useful breakpoints (1 / 10 / 100 / 1000) and walk each offer's actual
// ladder up to find the matching unit price; suppliers with sparse ladders
// (e.g. only qty=1 and qty=100) show the qty=1 price for "qty 10" because
// that's the price they'd actually charge at order time. Mirrors the
// pickBestUnitPrice logic in the Go backend.
const PRICING_PANEL_QTYS = [1, 10, 100, 1000]

function priceAtQty(breaks: { quantity: number; price: number }[], qty: number): number | null {
  if (breaks.length === 0) return null
  let best: { quantity: number; price: number } | null = null
  for (const b of breaks) {
    if (b.quantity > qty) continue
    if (best === null || b.quantity > best.quantity) best = b
  }
  if (best === null) {
    // No break at or below qty — fall back to the cheapest higher break.
    best = breaks.reduce((m, b) => (b.quantity < m.quantity ? b : m), breaks[0])
  }
  return best.price
}

export default function BomTable({ rows, onChange, onSaveMapping }: Props) {
  // Only one row's pricing panel is open at a time — opening another
  // collapses the previous. Keeps the table compact on dense BOMs.
  const [expandedRowId, setExpandedRowId] = useState<string | null>(null)

  function update(index: number, field: keyof BOMRow, value: BOMRow[keyof BOMRow]) {
    onChange(rows.map((r, i) => {
      if (i !== index) return r
      const updated = { ...r, [field]: value }
      // Editing a cross-reference cell is an explicit human declaration of the
      // value — auto-confirm to keep the workflow fast. Clearing a cell drops
      // it back out of the confirmed set so it returns to the Empty state.
      if (CONFIRMABLE_FIELDS.includes(field as ConfirmableField)) {
        const f = field as ConfirmableField
        const current = updated.confirmedFields ?? []
        const cleared = value === ''
        const next = cleared
          ? current.filter(x => x !== f)
          : current.includes(f) ? current : [...current, f]
        updated.confirmedFields = next
      }
      return updated
    }))
  }

  function updateQty(index: number, field: keyof Quantity, value: Quantity[keyof Quantity]) {
    onChange(rows.map((r, i) =>
      i === index ? { ...r, quantity: { ...r.quantity, [field]: value } } : r,
    ))
  }

  function confirmCell(index: number, field: ConfirmableField) {
    onChange(rows.map((r, i) => (i === index ? confirmField(r, field) : r)))
  }

  function confirmRow(index: number) {
    onChange(rows.map((r, i) => {
      if (i !== index) return r
      const toAdd = CONFIRMABLE_FIELDS.filter(f => isSuggested(r, f))
      if (toAdd.length === 0) return r
      return { ...r, confirmedFields: [...(r.confirmedFields ?? []), ...toAdd] }
    }))
  }

  function deleteRow(index: number) {
    onChange(
      rows
        .filter((_, i) => i !== index)
        .map((r, i) => ({ ...r, lineNumber: i + 1 })),
    )
  }

  function addRow() {
    const lineNumber = rows.length > 0 ? Math.max(...rows.map((r) => r.lineNumber)) + 1 : 1
    onChange([
      ...rows,
      {
        id: `manual-${Date.now()}`,
        lineNumber,
        rawLabel: '',
        description: '',
        quantity: { raw: '', value: 1, unit: 'EA', normalized: 1, flags: [] },
        customerPartNumber: '',
        internalPartNumber: '',
        manufacturerPartNumber: '',
        supplierReference: '',
        supplier: '',
        notes: '',
        confidence: 1,
        flags: [],
        confirmedFields: [],
      },
    ])
  }

  const suggestedCount = countSuggestedCells(rows)

  return (
    <div>
      <div style={toolbar}>
        <span style={{ color: '#6b7280', fontSize: 13 }}>
          {rows.length} {rows.length === 1 ? 'item' : 'items'}
          {suggestedCount > 0 && (
            <span style={{ marginLeft: 10, color: '#92400e' }}>
              · {suggestedCount} {suggestedCount === 1 ? 'cell needs' : 'cells need'} review
            </span>
          )}
        </span>
        <div style={{ display: 'flex', gap: 8 }}>
          {suggestedCount > 0 && (
            <button
              style={confirmAllBtn}
              onClick={() => onChange(confirmAllSuggestions(rows))}
              title="Mark every system suggestion as confirmed by the operator"
            >
              Confirm all suggestions ({suggestedCount})
            </button>
          )}
          <button style={addBtn} onClick={addRow}>
            + Add row
          </button>
        </div>
      </div>

      <div style={{ overflowX: 'auto', border: `1px solid ${colors.border}`, borderRadius: radius.lg }}>
        <table style={{ width: '100%', borderCollapse: 'collapse', fontSize: 13 }}>
          <thead>
            <tr>
              {COLUMNS.map((c) => (
                <th key={c.key} style={{ ...th, minWidth: c.width }}>
                  {c.label}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((row, i) => (
              <BomRow
                key={row.id}
                row={row}
                index={i}
                expanded={expandedRowId === row.id}
                onToggleExpand={() => setExpandedRowId(prev => prev === row.id ? null : row.id)}
                onUpdate={update}
                onUpdateQty={updateQty}
                onConfirmCell={confirmCell}
                onConfirmRow={confirmRow}
                onDelete={deleteRow}
                onSaveMapping={onSaveMapping}
              />
            ))}
          </tbody>
        </table>
      </div>
    </div>
  )
}

function BomRow({
  row, index, expanded, onToggleExpand, onUpdate, onUpdateQty, onConfirmCell, onConfirmRow, onDelete, onSaveMapping,
}: {
  row: BOMRow
  index: number
  expanded: boolean
  onToggleExpand: () => void
  onUpdate: (i: number, field: keyof BOMRow, value: BOMRow[keyof BOMRow]) => void
  onUpdateQty: (i: number, field: keyof Quantity, value: Quantity[keyof Quantity]) => void
  onConfirmCell: (i: number, field: ConfirmableField) => void
  onConfirmRow: (i: number) => void
  onDelete: (i: number) => void
  onSaveMapping: Props['onSaveMapping']
}) {
  const [mappingSaved, setMappingSaved] = useState(false)
  const [suggestions, setSuggestions] = useState<Mapping[]>([])
  const [showSuggestions, setShowSuggestions] = useState(false)
  const [loadingSuggestions, setLoadingSuggestions] = useState(false)
  const suggestRef = useRef<HTMLTableCellElement>(null)

  useEffect(() => {
    function handleClickOutside(e: MouseEvent) {
      if (suggestRef.current && !suggestRef.current.contains(e.target as Node)) {
        setShowSuggestions(false)
      }
    }
    document.addEventListener('mousedown', handleClickOutside)
    return () => document.removeEventListener('mousedown', handleClickOutside)
  }, [])

  async function handleSuggest() {
    const query = (row.description || row.customerPartNumber || '').trim()
    if (!query) return
    setLoadingSuggestions(true)
    setShowSuggestions(true)
    try {
      const results = await suggestMappings(query)
      setSuggestions(results)
    } finally {
      setLoadingSuggestions(false)
    }
  }

  function applySuggestion(m: Mapping) {
    if (m.internalPartNumber) onUpdate(index, 'internalPartNumber', m.internalPartNumber)
    if (m.manufacturerPartNumber) onUpdate(index, 'manufacturerPartNumber', m.manufacturerPartNumber)
    if (m.customerPartNumber && !row.customerPartNumber) onUpdate(index, 'customerPartNumber', m.customerPartNumber)
    setShowSuggestions(false)
  }

  async function handleSaveMapping() {
    await onSaveMapping({
      customerPartNumber: row.customerPartNumber,
      internalPartNumber: row.internalPartNumber,
      manufacturerPartNumber: row.manufacturerPartNumber,
      description: row.description,
      source: 'manual',
    })
    setMappingSaved(true)
    setTimeout(() => setMappingSaved(false), 2000)
  }

  const canSaveMapping = row.customerPartNumber.trim() !== ''
  const qtyAmbiguous = row.quantity.flags.includes('unit_ambiguous')
  const needsMapping = !row.internalPartNumber
  const rowHasSuggestions = CONFIRMABLE_FIELDS.some(f => isSuggested(row, f))

  return (
    <>
    <tr style={expanded ? { ...rowTint(row), background: '#f8f7ff' } : rowTint(row)}>
      <td style={{ ...td, color: '#9ca3af', textAlign: 'center', fontSize: 12 }}>
        {row.lineNumber}
      </td>
      <td style={td}>
        <input className="bom-input" value={row.rawLabel}
          onChange={(e) => onUpdate(index, 'rawLabel', e.target.value)} />
      </td>
      <td style={td}>
        <input className="bom-input" value={row.description}
          onChange={(e) => onUpdate(index, 'description', e.target.value)} />
      </td>
      {/* Raw quantity — preserved from drawing, editable for corrections */}
      <td style={{ ...td, position: 'relative' }}>
        <input
          className="bom-input"
          value={row.quantity.raw}
          onChange={(e) => onUpdateQty(index, 'raw', e.target.value)}
          style={{ fontFamily: 'monospace', fontSize: 12, color: qtyAmbiguous ? '#92400e' : '#374151' }}
        />
        {qtyAmbiguous && (
          <span title="Unit is ambiguous — verify before use"
            style={{ position: 'absolute', right: 4, top: '50%', transform: 'translateY(-50%)',
              color: '#f59e0b', fontSize: 14, pointerEvents: 'none' }}>
            ⚠
          </span>
        )}
      </td>
      {/* Parsed numeric value — editable */}
      <td style={td}>
        <input
          className="bom-input"
          type="number"
          min={0}
          step="any"
          value={row.quantity.value ?? ''}
          onChange={(e) => onUpdateQty(index, 'value', parseFloat(e.target.value) || null)}
          style={{ width: 56 }}
        />
      </td>
      <td style={td}>
        <input
          className="bom-input"
          value={row.quantity.unit ?? ''}
          onChange={(e) => onUpdateQty(index, 'unit', e.target.value || null)}
          style={{ width: 46 }}
        />
      </td>
      <td style={td}>
        <SuggestableCell
          value={row.customerPartNumber}
          suggested={isSuggested(row, 'customerPartNumber')}
          onChange={v => onUpdate(index, 'customerPartNumber', v)}
          onConfirm={() => onConfirmCell(index, 'customerPartNumber')}
        />
      </td>
      <td style={{ ...td, position: 'relative' }} ref={suggestRef}>
        <div style={{ display: 'flex', gap: 3, alignItems: 'center' }}>
          <SuggestableCell
            value={row.internalPartNumber}
            suggested={isSuggested(row, 'internalPartNumber')}
            onChange={v => onUpdate(index, 'internalPartNumber', v)}
            onConfirm={() => onConfirmCell(index, 'internalPartNumber')}
          />
          {needsMapping && (
            <button
              onClick={handleSuggest}
              title="Suggest mappings from description"
              style={suggestBtn}
            >
              {loadingSuggestions ? '…' : '?'}
            </button>
          )}
        </div>
        {row.suggestion && needsMapping && !showSuggestions && (
          <div style={catalogHint} title={row.suggestion.matchReasons.join(', ')}>
            Suggests <strong>{row.suggestion.internalPartNumber}</strong>
            <button
              style={catalogApplyBtn}
              onClick={() => {
                if (row.suggestion) {
                  onUpdate(index, 'internalPartNumber', row.suggestion.internalPartNumber)
                  if (row.suggestion.manufacturerPartNumber && !row.manufacturerPartNumber) {
                    onUpdate(index, 'manufacturerPartNumber', row.suggestion.manufacturerPartNumber)
                  }
                }
              }}
            >
              Apply
            </button>
          </div>
        )}
        {showSuggestions && (
          <div style={suggestPopover}>
            {suggestions.length === 0 && !loadingSuggestions && (
              <div style={suggestEmpty}>No matches found</div>
            )}
            {suggestions.map(m => (
              <button key={m.customerPartNumber} style={suggestItem} onClick={() => applySuggestion(m)}>
                <span style={{ fontWeight: 600, color: colors.text, fontSize: 12 }}>
                  {m.internalPartNumber || m.customerPartNumber}
                </span>
                {m.description && (
                  <span style={{ color: colors.textMuted, fontSize: 11, marginLeft: 6 }}>
                    {m.description.length > 40 ? m.description.slice(0, 40) + '…' : m.description}
                  </span>
                )}
              </button>
            ))}
          </div>
        )}
      </td>
      <td style={td}>
        <SuggestableCell
          value={row.manufacturerPartNumber}
          suggested={isSuggested(row, 'manufacturerPartNumber')}
          onChange={v => onUpdate(index, 'manufacturerPartNumber', v)}
          onConfirm={() => onConfirmCell(index, 'manufacturerPartNumber')}
        />
      </td>
      <td style={td}>
        <SupplierCell refCode={row.supplierReference} supplier={row.supplier} />
      </td>
      <td style={td}>
        <NotesCell notes={row.notes} />
      </td>
      <td style={td}>
        <ConfidenceBadge value={row.confidence} />
      </td>
      <td style={td}>
        <PricingCell
          pricing={row.pricing}
          mpn={row.manufacturerPartNumber}
          flags={row.flags}
          expanded={expanded}
          onToggleExpand={onToggleExpand}
        />
      </td>
      <td style={td}>
        <FlagList flags={row.flags} />
      </td>
      <td style={{ ...td, textAlign: 'center', whiteSpace: 'nowrap' }}>
        {rowHasSuggestions && (
          <button
            onClick={() => onConfirmRow(index)}
            title="Confirm all system suggestions on this row"
            style={confirmRowBtn}
          >
            ✓ Confirm
          </button>
        )}
        {canSaveMapping && (
          <button
            onClick={handleSaveMapping}
            title="Save as mapping for future use"
            style={mappingSaved ? savedMappingBtn : saveMappingBtn}
          >
            {mappingSaved ? '✓' : '↗'}
          </button>
        )}
        <button onClick={() => onDelete(index)} title="Remove row" style={deleteBtn}>
          ×
        </button>
      </td>
    </tr>
    {expanded && row.pricing && (
      <tr>
        <td colSpan={COLUMNS.length} style={pricingPanelCell}>
          <PricingDetailsPanel pricing={row.pricing} mpn={row.manufacturerPartNumber} rowQty={row.quantity.value ?? 1} />
        </td>
      </tr>
    )}
    </>
  )
}

// SuggestableCell renders a cross-reference cell that is either Confirmed
// (plain) or Suggested (italic + amber background with a click-to-confirm
// tick). Editing on blur is handled by the parent — the input always reports
// changes via onChange so the parent's auto-confirm-on-edit can take effect.
function SuggestableCell({
  value, suggested, onChange, onConfirm,
}: {
  value: string
  suggested: boolean
  onChange: (v: string) => void
  onConfirm: () => void
}) {
  if (!suggested) {
    return (
      <input
        className="bom-input"
        value={value}
        onChange={e => onChange(e.target.value)}
      />
    )
  }
  return (
    <div style={{ display: 'flex', alignItems: 'center', gap: 2, position: 'relative' }}>
      <input
        className="bom-input"
        value={value}
        onChange={e => onChange(e.target.value)}
        title="System suggestion — confirm or edit before export"
        style={{
          fontStyle: 'italic',
          background: '#fffbeb',
          color: '#92400e',
          borderColor: '#fcd34d',
        }}
      />
      <button
        type="button"
        onClick={onConfirm}
        title="Confirm this suggestion as-is"
        style={confirmTick}
      >
        ✓
      </button>
    </div>
  )
}

function SupplierCell({ refCode, supplier }: { refCode: string; supplier: string }) {
  if (!refCode) return <span style={{ color: '#d1d5db', fontSize: 12 }}>—</span>

  const colors: Record<string, { bg: string; color: string }> = {
    RS:      { bg: '#dbeafe', color: '#1e40af' },
    Farnell: { bg: '#fce7f3', color: '#9d174d' },
    Unknown: { bg: '#f3f4f6', color: '#4b5563' },
  }
  const c = colors[supplier] ?? colors.Unknown

  return (
    <div style={{ display: 'flex', flexDirection: 'column', gap: 2 }}>
      {supplier && (
        <span style={{ padding: '1px 6px', borderRadius: 3, fontSize: 11, fontWeight: 600,
          background: c.bg, color: c.color, display: 'inline-block', width: 'fit-content' }}>
          {supplier}
        </span>
      )}
      <span style={{ fontSize: 12, fontFamily: 'monospace', color: '#374151' }}>{refCode}</span>
    </div>
  )
}

function NotesCell({ notes }: { notes: string }) {
  if (!notes) return <span style={{ color: '#d1d5db', fontSize: 12 }}>—</span>
  return (
    <span title={notes} style={{ fontSize: 12, color: '#4b5563', cursor: 'help' }}>
      {notes.length > 40 ? notes.slice(0, 40) + '…' : notes}
    </span>
  )
}

function ConfidenceBadge({ value }: { value: number }) {
  const pct = Math.round(value * 100)
  const [bg, color] =
    value >= 0.85 ? ['#d1fae5', '#065f46'] :
    value >= 0.65 ? ['#fef3c7', '#92400e'] :
                    ['#fee2e2', '#991b1b']
  return (
    <span style={{ display: 'inline-block', padding: '2px 6px', borderRadius: 10,
      fontSize: 12, fontWeight: 600, background: bg, color }}>
      {pct}%
    </span>
  )
}

const FLAG_CONFIG: Record<string, { label: string; bg: string; color: string }> = {
  'unit_ambiguous':              { label: 'unit?',      bg: '#fef3c7', color: '#92400e' },
  'supplier_reference_detected': { label: 'supplier',   bg: '#dbeafe', color: '#1e40af' },
  'missing_part_number':         { label: 'no MPN',     bg: '#fee2e2', color: '#991b1b' },
  'mapping_applied':             { label: 'mapped',     bg: '#d1fae5', color: '#065f46' },
  'low_confidence':              { label: 'low conf',   bg: '#fee2e2', color: '#991b1b' },
  'needs-review':                { label: 'review',     bg: '#fef3c7', color: '#78350f' },
  'dimension-estimated':         { label: 'estimated',  bg: '#ede9fe', color: '#5b21b6' },
  'missing-manufacturer-pn':     { label: 'no MPN',     bg: '#fee2e2', color: '#991b1b' },
  'ambiguous-spec':              { label: 'ambiguous',  bg: '#fef3c7', color: '#92400e' },
  'pricing_unavailable':         { label: 'no price',   bg: '#fee2e2', color: '#991b1b' },
}

// HIGH_PRICE_WARN_THRESHOLD trips an amber visual + tooltip on the compact
// pricing cell. Picked at £1000 because most parts on these BOMs are well
// under that — anything past it is either a high-value assembly (rare) or a
// data quirk (full-reel price reported at qty 1, wrong-currency conversion).
// Crossing it doesn't suppress the value; it just nudges the operator to
// double-check before pasting into SAP.
const HIGH_PRICE_WARN_THRESHOLD = 1000

// PricingCell: two-line compact rendering (price on top, supplier on the
// next line) plus an expand chevron. Click the chevron OR the cell body to
// toggle the row's pricing-details panel. The supplier URL moves into the
// details panel — keeping the compact cell click-target for expansion.
function PricingCell({
  pricing, mpn, flags, expanded, onToggleExpand,
}: {
  pricing?: RowPricing
  mpn: string
  flags: string[]
  expanded: boolean
  onToggleExpand: () => void
}) {
  if (pricing && pricing.bestUnitPrice) {
    const sup = pricing.bestStockSupplier || pricing.offers[0]?.supplier
    const price = pricing.bestUnitPrice
    const high = price.amount > HIGH_PRICE_WARN_THRESHOLD
    return (
      <button
        type="button"
        onClick={onToggleExpand}
        style={pricingCellButton}
        title={
          high
            ? `Price is unusually high (>${HIGH_PRICE_WARN_THRESHOLD}) — verify before pasting into SAP. Click to see all ${pricing.offers.length} offer(s).`
            : `${pricing.offers.length} offer${pricing.offers.length === 1 ? '' : 's'} — click to ${expanded ? 'collapse' : 'expand'}`
        }
      >
        <span style={{ display: 'flex', alignItems: 'baseline', gap: 4 }}>
          <span style={{
            fontFamily: 'monospace', fontWeight: 600,
            color: high ? '#92400e' : colors.text,
          }}>
            {formatMoney(price.amount, price.currency)}
          </span>
          {high && <span style={{ color: '#b45309', fontSize: 11 }}>⚠</span>}
          <ExpandCaret expanded={expanded} />
        </span>
        {sup && (
          <span style={{ color: colors.textMuted, fontSize: 11, lineHeight: 1.1 }}>{sup}</span>
        )}
      </button>
    )
  }
  if (flags.includes(FLAG_PRICING_UNAVAILABLE)) {
    return <span style={{ color: '#991b1b', fontSize: 12 }} title="Pricing run found no offers for this MPN">No price</span>
  }
  return (
    <span style={{ color: '#d1d5db', fontSize: 12 }}
      title={mpn.trim() === '' ? 'No MPN to price' : 'Not yet priced — click "Price BOM" in the toolbar'}>
      —
    </span>
  )
}

function ExpandCaret({ expanded }: { expanded: boolean }) {
  return (
    <svg width="9" height="9" viewBox="0 0 12 12" aria-hidden="true"
      style={{ transform: expanded ? 'rotate(90deg)' : 'rotate(0deg)', transition: 'transform 0.12s', color: '#9ca3af' }}>
      <path d="M4 2 L8 6 L4 10" stroke="currentColor" strokeWidth="1.5" fill="none" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  )
}

// PricingDetailsPanel is the inline expansion that opens under a BOM row.
// Renders one row per offer (so two offers from the same supplier appear
// as two rows), four fixed qty columns walked up each offer's break ladder,
// stock + lead time on the left, click-through link on the right. Cheapest
// price in each qty column is highlighted; the offer whose qty=1 price is
// cheapest is sorted first.
function PricingDetailsPanel({ pricing, mpn, rowQty }: { pricing: RowPricing; mpn: string; rowQty: number }) {
  // Sort offers by ascending qty=1 price so the operator's eye lands on the
  // cheapest entry first. Out-of-stock offers (Stock === 0) sink to the
  // bottom regardless of price — Andrew can't order what nobody has.
  const offers = [...pricing.offers].sort((a, b) => {
    const aOut = a.stock === 0
    const bOut = b.stock === 0
    if (aOut !== bOut) return aOut ? 1 : -1
    const pa = priceAtQty(a.priceBreaks, 1) ?? Number.POSITIVE_INFINITY
    const pb = priceAtQty(b.priceBreaks, 1) ?? Number.POSITIVE_INFINITY
    return pa - pb
  })

  // Compute the cheapest offer per qty column so we can highlight it.
  const cheapestByQty: Record<number, number | null> = {}
  for (const q of PRICING_PANEL_QTYS) {
    let min: number | null = null
    for (const o of offers) {
      const p = priceAtQty(o.priceBreaks, q)
      if (p !== null && (min === null || p < min)) min = p
    }
    cheapestByQty[q] = min
  }

  const fetched = formatRelativeTime(pricing.fetchedAt)
  const bestPriceLabel = pricing.bestUnitPrice
    ? `${formatMoney(pricing.bestUnitPrice.amount, pricing.bestUnitPrice.currency)} @ qty ${rowQty}`
    : '—'

  return (
    <div style={panelWrap}>
      <div style={panelHeader}>
        <span style={{ fontFamily: 'monospace', fontWeight: 600 }}>{mpn || 'unknown MPN'}</span>
        <span style={{ color: colors.textMuted }}>· {offers.length} offer{offers.length === 1 ? '' : 's'}</span>
        <span style={{ color: colors.textMuted }}>· best {bestPriceLabel}</span>
        <span style={{ color: colors.textSubtle, marginLeft: 'auto' }}>fetched {fetched}</span>
      </div>
      <table style={panelTable}>
        <thead>
          <tr>
            <th style={panelTh}>Supplier</th>
            <th style={{ ...panelTh, textAlign: 'right' }}>Stock</th>
            <th style={{ ...panelTh, textAlign: 'right' }}>Lead</th>
            {PRICING_PANEL_QTYS.map(q => (
              <th key={q} style={{ ...panelTh, textAlign: 'right' }}>qty {q}</th>
            ))}
            <th style={panelTh}></th>
          </tr>
        </thead>
        <tbody>
          {offers.map((o, i) => {
            const outOfStock = o.stock === 0
            return (
              <tr key={`${o.supplier}-${o.sku}-${i}`} style={outOfStock ? { opacity: 0.55 } : undefined}>
                <td style={panelTd}>
                  <span style={{ fontWeight: 600 }}>{o.supplier}</span>
                  <span style={{ color: colors.textSubtle, fontSize: 11, marginLeft: 6, fontFamily: 'monospace' }}>
                    {o.sku}
                  </span>
                </td>
                <td style={{ ...panelTd, textAlign: 'right', fontFamily: 'monospace' }}>
                  {o.stock != null ? o.stock.toLocaleString() : <span style={{ color: colors.textSubtle }}>—</span>}
                </td>
                <td style={{ ...panelTd, textAlign: 'right', fontFamily: 'monospace' }}>
                  {o.leadTimeDays != null ? `${o.leadTimeDays}d` : <span style={{ color: colors.textSubtle }}>—</span>}
                </td>
                {PRICING_PANEL_QTYS.map(q => {
                  const p = priceAtQty(o.priceBreaks, q)
                  const isCheapest = p !== null && cheapestByQty[q] !== null && Math.abs(p - cheapestByQty[q]!) < 1e-9
                  return (
                    <td key={q} style={{
                      ...panelTd, textAlign: 'right', fontFamily: 'monospace',
                      ...(isCheapest ? { background: '#ecfdf5', color: '#065f46', fontWeight: 600 } : {}),
                    }}>
                      {p !== null ? formatMoney(p, o.currency) : <span style={{ color: colors.textSubtle }}>—</span>}
                    </td>
                  )
                })}
                <td style={{ ...panelTd, textAlign: 'right' }}>
                  {o.supplierUrl ? (
                    <a href={o.supplierUrl} target="_blank" rel="noreferrer noopener"
                      style={{ fontSize: 11, color: colors.brand, textDecoration: 'none' }}
                      title={`Open ${o.supplier} listing`}>
                      view ↗
                    </a>
                  ) : null}
                </td>
              </tr>
            )
          })}
        </tbody>
      </table>
    </div>
  )
}

// formatRelativeTime — duplicated from MappingSearch for now; pulling into a
// shared module would mean a new file, and this isn't a behavioural concern.
function formatRelativeTime(iso: string): string {
  const t = new Date(iso).getTime()
  if (Number.isNaN(t)) return '—'
  const diff = Date.now() - t
  const s = Math.floor(diff / 1000)
  if (s < 60) return 'just now'
  const m = Math.floor(s / 60); if (m < 60) return `${m}m ago`
  const h = Math.floor(m / 60); if (h < 24) return `${h}h ago`
  const d = Math.floor(h / 24); if (d < 30) return `${d}d ago`
  return `${Math.floor(d / 30)}mo ago`
}

// formatMoney renders an amount with a decimal count tuned to the magnitude:
// ≥ £1 → 2 places (£7,963.12, not £7,963.1172); < £1 → 4 places at most so a
// 2.6p resistor reads as £0.026, not £0.03. Trailing zeros in the sub-unit
// case are trimmed so a £0.30 doesn't render as £0.3000.
function formatMoney(amount: number, currency: string): string {
  const subUnit = Math.abs(amount) < 1 && amount !== 0
  const opts = subUnit
    ? { minimumFractionDigits: 2, maximumFractionDigits: 4 }
    : { minimumFractionDigits: 2, maximumFractionDigits: 2 }
  try {
    return new Intl.NumberFormat(undefined, { style: 'currency', currency, ...opts }).format(amount)
  } catch {
    return `${amount.toFixed(subUnit ? 4 : 2)} ${currency}`
  }
}

function FlagList({ flags }: { flags: string[] }) {
  if (!flags.length) return <span style={{ color: '#d1d5db', fontSize: 12 }}>—</span>
  return (
    <div style={{ display: 'flex', flexWrap: 'wrap', gap: 3 }}>
      {flags.map((f) => {
        const cfg = FLAG_CONFIG[f]
        const style = cfg
          ? { background: cfg.bg, color: cfg.color, fontWeight: 600 }
          : { background: '#f3f4f6', color: '#6b7280' }
        return (
          <span key={f} style={{ padding: '1px 5px', borderRadius: 3, fontSize: 11,
            whiteSpace: 'nowrap', ...style }}>
            {cfg ? cfg.label : f}
          </span>
        )
      })}
    </div>
  )
}

// Cell-level state has replaced row-level uncertainty tinting. Only quantity
// issues still tint the row — they are not yet expressed as a cell state.
function rowTint(row: BOMRow): CSSProperties {
  if (row.quantity.flags.includes('unit_ambiguous')) return { background: '#fefdf5' }
  return {}
}

const th: CSSProperties = {
  padding:       '9px 8px',
  background:    colors.bg,
  borderBottom:  `2px solid ${colors.border}`,
  textAlign:     'left',
  fontWeight:    600,
  color:         colors.textMuted,
  fontSize:      11,
  whiteSpace:    'nowrap',
  textTransform: 'uppercase',
  letterSpacing: '0.05em',
}

const td: CSSProperties = {
  padding:       '5px 8px',
  borderBottom:  `1px solid ${colors.borderLight}`,
  verticalAlign: 'middle',
}

// ── PricingCell + PricingDetailsPanel styles ────────────────────────────────

const pricingCellButton: CSSProperties = {
  display:       'flex',
  flexDirection: 'column',
  alignItems:    'flex-start',
  gap:           1,
  width:         '100%',
  padding:       '2px 4px',
  background:    'transparent',
  border:        '1px solid transparent',
  borderRadius:  radius.sm,
  cursor:        'pointer',
  textAlign:     'left',
  fontFamily:    'inherit',
  fontSize:      12,
  color:         'inherit',
}

const pricingPanelCell: CSSProperties = {
  background:   '#fafaff',
  borderTop:    `1px solid ${colors.border}`,
  borderBottom: `1px solid ${colors.borderLight}`,
  padding:      '8px 16px 12px',
}

const panelWrap: CSSProperties = {
  display: 'flex', flexDirection: 'column', gap: 6,
}

const panelHeader: CSSProperties = {
  display: 'flex', alignItems: 'baseline', gap: 6,
  fontSize: 12, color: colors.text,
}

const panelTable: CSSProperties = {
  width: '100%', borderCollapse: 'collapse', fontSize: 12,
  background: colors.surface, border: `1px solid ${colors.borderLight}`, borderRadius: radius.sm,
}

const panelTh: CSSProperties = {
  textAlign: 'left',
  padding: '5px 8px',
  borderBottom: `1px solid ${colors.borderLight}`,
  fontSize: 11, fontWeight: 600, color: colors.textMuted,
  textTransform: 'uppercase', letterSpacing: '0.04em',
  background: colors.bg,
}

const panelTd: CSSProperties = {
  padding: '5px 8px',
  borderBottom: `1px solid ${colors.borderLight}`,
}

const toolbar: CSSProperties = {
  display:        'flex',
  alignItems:     'center',
  justifyContent: 'space-between',
  padding:        '0 0 8px',
}

const addBtn: CSSProperties = {
  padding:      '5px 12px',
  fontSize:     13,
  background:   'transparent',
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.sm,
  cursor:       'pointer',
  color:        colors.textMuted,
}

const deleteBtn: CSSProperties = {
  padding:      '2px 6px',
  fontSize:     14,
  lineHeight:   1,
  background:   'transparent',
  border:       'none',
  borderRadius: radius.sm,
  cursor:       'pointer',
  color:        colors.textSubtle,
  marginLeft:   2,
}

const saveMappingBtn: CSSProperties = {
  padding:      '2px 6px',
  fontSize:     13,
  lineHeight:   1,
  background:   'transparent',
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.sm,
  cursor:       'pointer',
  color:        colors.textMuted,
}

const savedMappingBtn: CSSProperties = {
  ...saveMappingBtn,
  background:  colors.successBg,
  color:       colors.successText,
  borderColor: colors.successBorder,
}

const suggestBtn: CSSProperties = {
  flexShrink:   0,
  width:        20,
  height:       20,
  padding:      0,
  fontSize:     11,
  fontWeight:   700,
  lineHeight:   1,
  background:   colors.brandLight,
  color:        colors.brand,
  border:       `1px solid ${colors.brand}`,
  borderRadius: radius.sm,
  cursor:       'pointer',
}

const suggestPopover: CSSProperties = {
  position:     'absolute',
  top:          '100%',
  left:         0,
  zIndex:       200,
  background:   colors.surface,
  border:       `1px solid ${colors.border}`,
  borderRadius: radius.md,
  boxShadow:    '0 4px 12px rgba(0,0,0,0.1)',
  minWidth:     260,
  maxWidth:     340,
  overflow:     'hidden',
}

const suggestItem: CSSProperties = {
  display:     'flex',
  alignItems:  'center',
  width:       '100%',
  padding:     '7px 10px',
  background:  'none',
  border:      'none',
  borderBottom: `1px solid ${colors.borderLight}`,
  cursor:      'pointer',
  textAlign:   'left',
}

const suggestEmpty: CSSProperties = {
  padding:   '10px',
  fontSize:  12,
  color:     colors.textMuted,
  textAlign: 'center',
}

const confirmAllBtn: CSSProperties = {
  padding:      '5px 12px',
  fontSize:     13,
  fontWeight:   600,
  background:   '#fef3c7',
  color:        '#92400e',
  border:       '1px solid #fcd34d',
  borderRadius: radius.sm,
  cursor:       'pointer',
}

const confirmRowBtn: CSSProperties = {
  padding:      '2px 8px',
  fontSize:     11,
  fontWeight:   600,
  background:   '#fef3c7',
  color:        '#92400e',
  border:       '1px solid #fcd34d',
  borderRadius: radius.sm,
  cursor:       'pointer',
  marginRight:  4,
}

const confirmTick: CSSProperties = {
  flexShrink:   0,
  width:        20,
  height:       20,
  padding:      0,
  fontSize:     11,
  fontWeight:   700,
  lineHeight:   1,
  background:   '#fef3c7',
  color:        '#92400e',
  border:       '1px solid #fcd34d',
  borderRadius: radius.sm,
  cursor:       'pointer',
}

const catalogHint: CSSProperties = {
  position:     'absolute',
  top:          '100%',
  left:         0,
  marginTop:    2,
  padding:      '4px 8px',
  background:   colors.brandLight,
  color:        colors.brand,
  border:       `1px solid ${colors.brand}`,
  borderRadius: radius.sm,
  fontSize:     11,
  zIndex:       100,
  display:      'flex',
  alignItems:   'center',
  gap:          6,
  whiteSpace:   'nowrap',
}

const catalogApplyBtn: CSSProperties = {
  padding:      '1px 6px',
  fontSize:     11,
  fontWeight:   600,
  background:   colors.brand,
  color:        '#fff',
  border:       'none',
  borderRadius: radius.sm,
  cursor:       'pointer',
}
