export type DocumentStatus = 'uploaded' | 'analyzing' | 'done' | 'error'

export interface Quantity {
  raw: string
  value: number | null
  unit: string | null
  normalized: number | null
  flags: string[]
}

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
}

export interface Document {
  id: string
  filename: string
  status: DocumentStatus
  uploadedAt: string
  bomRows: BOMRow[]
  warnings: string[]
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
  customerPartNumber: string
  internalPartNumber: string
  manufacturerPartNumber: string
  description: string
  source: string    // "manual" | "inferred" | "csv-upload"
  confidence: number
  lastUsedAt: string
  createdAt: string
  updatedAt: string
}
