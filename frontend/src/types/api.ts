export type DocumentStatus = 'uploaded' | 'analyzing' | 'done' | 'error'

export interface Quantity {
  raw: string
  value: number | null
  unit: string | null
  normalized: number | null
  flags: string[]
}

export interface PartSuggestion {
  catalogPartId: string
  internalPartNumber: string
  manufacturerPartNumber?: string
  score: number
  source: string  // "exact_mpn" | "fingerprint"
  matchReasons: string[]
}

// Cross-reference cell field names — values used in BOMRow.confirmedFields.
export type ConfirmableField = 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber'
export const CONFIRMABLE_FIELDS: ConfirmableField[] = [
  'customerPartNumber', 'internalPartNumber', 'manufacturerPartNumber',
]

export interface BOMRow {
  id: string
  lineNumber: number
  rawLabel: string
  description: string
  quantity: Quantity
  customerPartNumber: string
  internalPartNumber: string
  manufacturerPartNumber: string
  supplierReference: string
  supplier: string  // "RS" | "Farnell" | "Unknown" | ""
  notes: string
  confidence: number  // 0.0–1.0
  flags: string[]
  suggestion?: PartSuggestion
  // Cells (by JSON field name) the operator or a stored mapping has confirmed.
  // A non-empty cell not in this list is a system Suggestion awaiting review.
  confirmedFields: string[]
  // Joined at read time from the part_prices cache; nil when the row has
  // never been priced or the join produced no offers. Not persisted on the
  // BOM row.
  pricing?: RowPricing
}

export interface RowPricing {
  offers: SupplierOffer[]
  bestUnitPrice?: Money
  bestStockSupplier?: string
  fetchedAt: string
}

export interface SupplierOffer {
  supplier: string
  sku: string
  priceBreaks: PriceBreak[]
  stock?: number
  leadTimeDays?: number
  supplierUrl: string
  source: string  // "nexar" | "csv" | "manual" | "mock"
  currency: string
  fetchedAt: string
}

export interface PriceBreak {
  quantity: number
  price: number
}

export interface Money {
  amount: number
  currency: string
}

export interface PricingRun {
  id: string
  documentId: string
  startedAt: string
  completedAt?: string
  rowsTotal: number
  rowsPriced: number
  rowsUnavailable: number
  rowsSkipped: number
  nexarCallsMade: number
  cacheHits: number
  currency: string
  errorMessage?: string
}

// Flag set on rows where pricing completed with no offers from any source.
export const FLAG_PRICING_UNAVAILABLE = 'pricing_unavailable'

export interface Document {
  id: string
  filename: string
  status: DocumentStatus
  uploadedAt: string
  bomRows: BOMRow[]
  warnings: string[]
  clonedFromId?: string
  fileSizeBytes: number
  analysisDurationMs?: number
  errorMessage?: string
  // Optional client tag — when set, mapping lookups prefer this client's
  // bucket and fall back to the generic bucket.
  clientLabel: string
  // Most recent pricing run for this document, joined at read time from
  // the pricing_runs table. Undefined when the BOM has never been priced.
  lastPricingRun?: PricingRun
}

export interface ClientMappingSummary {
  label: string  // empty = generic / untagged bucket
  count: number
}

export interface MappingImportResult {
  saved: number
  overwritten: number
  skipped: number
}

// Row shape submitted to POST /api/mappings/import after client-side Excel parsing.
export interface MappingImportRow {
  customerPartNumber: string
  internalPartNumber: string
  manufacturerPartNumber: string
  description: string
}

export interface ScoreBreakdown {
  filename: number
  cpn: number
  mpn: number
}

export interface SimilarDocument {
  id: string
  filename: string
  uploadedAt: string
  score: number
  scoreBreakdown: ScoreBreakdown
  matchReasons: string[]
  bomRowCount: number
}

export interface MatchFeedback {
  drawingId: string
  candidateId: string
  action: 'accept' | 'reject'
  score: number
  scoreBreakdown?: ScoreBreakdown
}

export interface BOMPreview {
  filename: string
  rows: BOMRow[]
  totalRows: number
}

export interface ExportConfig {
  columns: string[]
  includeHeader: boolean
}

export interface ErrorLogEntry {
  timestamp: string
  level: string      // "error" | "warn"
  component: string  // e.g. "analysis"
  message: string
  docName?: string
}

export interface Mapping {
  id: string
  clientLabel: string  // "" = generic / pooled bucket
  customerPartNumber: string
  internalPartNumber: string
  manufacturerPartNumber: string
  description: string
  source: string    // "manual" | "inferred" | "csv-upload" | "excel-import"
  confidence: number
  lastUsedAt: string
  createdAt: string
  updatedAt: string
}
