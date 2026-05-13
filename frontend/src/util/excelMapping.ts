import type { MappingImportRow } from '../types/api'

// Operator-provided Excel files won't have one canonical header form. Accept
// any of these (case-insensitive, whitespace-trimmed) per canonical field.
const HEADER_SYNONYMS: Record<keyof MappingImportRow, string[]> = {
  customerPartNumber: [
    'customer p/n', 'customer part number', 'customer part no', 'customer pn',
    'cust pn', 'customer code', 'customer part', 'cust part number',
  ],
  internalPartNumber: [
    'internal p/n', 'internal part number', 'internal part no', 'internal pn',
    'our part number', 'stock code', 'sku', 'internal code',
  ],
  manufacturerPartNumber: [
    'manufacturer p/n', 'manufacturer part number', 'manufacturer part no',
    'mfr part number', 'mfr p/n', 'mpn', 'manufacturer code',
  ],
  description: ['description', 'part description', 'desc'],
}

export interface ParsedExcel {
  rows: MappingImportRow[]
  recognisedColumns: string[]
  ignoredColumns: string[]
  rowCount: number
}

function normHeader(h: string): string {
  return h.toLowerCase().trim().replace(/\s+/g, ' ')
}

function detectColumnMap(headers: string[]): { map: Partial<Record<keyof MappingImportRow, string>>; ignored: string[] } {
  const map: Partial<Record<keyof MappingImportRow, string>> = {}
  const used = new Set<string>()
  for (const [field, synonyms] of Object.entries(HEADER_SYNONYMS) as [keyof MappingImportRow, string[]][]) {
    const match = headers.find(h => synonyms.includes(normHeader(h)))
    if (match) {
      map[field] = match
      used.add(match)
    }
  }
  const ignored = headers.filter(h => !used.has(h))
  return { map, ignored }
}

// parseMappingExcel reads an .xlsx file and returns the canonical rows plus a
// summary of which columns were recognised vs ignored. The caller decides
// whether to surface the ignored columns as a warning.
export async function parseMappingExcel(file: File): Promise<ParsedExcel> {
  // Dynamically import xlsx — the SheetJS bundle is ~340KB and is only needed
  // when an operator actually triggers an Excel import. Keeps the initial
  // page load slim for the common BOM-review workflow.
  const XLSX = await import('xlsx')
  const buf = await file.arrayBuffer()
  const wb = XLSX.read(buf, { type: 'array' })
  if (wb.SheetNames.length === 0) {
    return { rows: [], recognisedColumns: [], ignoredColumns: [], rowCount: 0 }
  }
  const ws = wb.Sheets[wb.SheetNames[0]]
  const raw = XLSX.utils.sheet_to_json<Record<string, unknown>>(ws, { defval: '' })

  // Headers come from the first row's keys (sheet_to_json infers from header row).
  const headers = raw.length > 0 ? Object.keys(raw[0]) : []
  const { map, ignored } = detectColumnMap(headers)

  const rows: MappingImportRow[] = raw
    .map(r => ({
      customerPartNumber:     map.customerPartNumber     ? String(r[map.customerPartNumber] ?? '').trim()     : '',
      internalPartNumber:     map.internalPartNumber     ? String(r[map.internalPartNumber] ?? '').trim()     : '',
      manufacturerPartNumber: map.manufacturerPartNumber ? String(r[map.manufacturerPartNumber] ?? '').trim() : '',
      description:            map.description            ? String(r[map.description] ?? '').trim()            : '',
    }))
    .filter(r => r.customerPartNumber !== '')

  return {
    rows,
    recognisedColumns: Object.values(map),
    ignoredColumns: ignored,
    rowCount: raw.length,
  }
}
