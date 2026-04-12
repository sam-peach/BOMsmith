import { type CSSProperties, useState } from 'react'
import type { BOMRow, Mapping, Quantity } from '../types/api'
import { colors, radius } from '../theme'

interface Props {
  rows: BOMRow[]
  onChange: (rows: BOMRow[]) => void
  onSaveMapping: (mapping: Pick<Mapping, 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber' | 'description' | 'source'>) => Promise<void>
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
  { key: 'flags',                  label: 'Flags',        width: 160 },
  { key: '_actions',               label: '',             width: 60  },
]

export default function BomTable({ rows, onChange, onSaveMapping }: Props) {
  function update(index: number, field: keyof BOMRow, value: BOMRow[keyof BOMRow]) {
    onChange(rows.map((r, i) => (i === index ? { ...r, [field]: value } : r)))
  }

  function updateQty(index: number, field: keyof Quantity, value: Quantity[keyof Quantity]) {
    onChange(rows.map((r, i) =>
      i === index ? { ...r, quantity: { ...r.quantity, [field]: value } } : r,
    ))
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
      },
    ])
  }

  return (
    <div>
      <div style={toolbar}>
        <span style={{ color: '#6b7280', fontSize: 13 }}>
          {rows.length} {rows.length === 1 ? 'item' : 'items'}
        </span>
        <button style={addBtn} onClick={addRow}>
          + Add row
        </button>
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
                onUpdate={update}
                onUpdateQty={updateQty}
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
  row, index, onUpdate, onUpdateQty, onDelete, onSaveMapping,
}: {
  row: BOMRow
  index: number
  onUpdate: (i: number, field: keyof BOMRow, value: BOMRow[keyof BOMRow]) => void
  onUpdateQty: (i: number, field: keyof Quantity, value: Quantity[keyof Quantity]) => void
  onDelete: (i: number) => void
  onSaveMapping: Props['onSaveMapping']
}) {
  const [mappingSaved, setMappingSaved] = useState(false)

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

  return (
    <tr style={rowTint(row)}>
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
        <input className="bom-input" value={row.customerPartNumber}
          onChange={(e) => onUpdate(index, 'customerPartNumber', e.target.value)} />
      </td>
      <td style={td}>
        <input className="bom-input" value={row.internalPartNumber}
          onChange={(e) => onUpdate(index, 'internalPartNumber', e.target.value)} />
      </td>
      <td style={td}>
        <input className="bom-input" value={row.manufacturerPartNumber}
          onChange={(e) => onUpdate(index, 'manufacturerPartNumber', e.target.value)} />
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
        <FlagList flags={row.flags} />
      </td>
      <td style={{ ...td, textAlign: 'center', whiteSpace: 'nowrap' }}>
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

function rowTint(row: BOMRow): CSSProperties {
  if (row.confidence < 0.65) return { background: '#fff5f5' }
  if (row.flags.includes('unit_ambiguous') || row.flags.includes('missing_part_number'))
    return { background: '#fefdf5' }
  if (row.flags.length > 0) return { background: colors.bg }
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
