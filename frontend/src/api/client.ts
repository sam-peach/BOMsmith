import type { BOMPreview, BOMRow, Document, ErrorLogEntry, ExportConfig, MatchFeedback, Mapping, SimilarDocument } from '../types/api'

const BASE = '/api'

async function parseError(res: Response): Promise<string> {
  try {
    const body = await res.json()
    return body.error ?? `HTTP ${res.status}`
  } catch {
    return `HTTP ${res.status}`
  }
}

export async function uploadDocument(file: File): Promise<Document> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch(`${BASE}/documents/upload`, { method: 'POST', body: form })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function analyzeDocument(id: string): Promise<Document> {
  // Kick off async analysis — server returns 202 immediately.
  const res = await fetch(`${BASE}/documents/${id}/analyze`, { method: 'POST' })
  if (!res.ok) throw new Error(await parseError(res))
  return waitForAnalysis(id)
}

// waitForAnalysis polls GET /documents/{id} until analysis completes.
// Used both by analyzeDocument (after POST) and to resume polling on page load.
export async function waitForAnalysis(id: string): Promise<Document> {
  const pollIntervalMs = 2000
  const timeoutMs = 6 * 60 * 1000 // 6 minutes (matches server-side LLM timeout)
  const deadline = Date.now() + timeoutMs
  while (Date.now() < deadline) {
    await new Promise(resolve => setTimeout(resolve, pollIntervalMs))
    let doc: Document
    try {
      doc = await getDocument(id)
    } catch {
      // 404 means the document was deleted (cancelled) while we were polling.
      throw new Error('Cancelled')
    }
    if (doc.status === 'done') return doc
    if (doc.status === 'error') throw new Error(doc.errorMessage ?? 'Analysis failed')
  }
  throw new Error('Analysis timed out — the drawing may be too large. Try splitting it into sections.')
}

export async function listDocuments(): Promise<Document[]> {
  const res = await fetch(`${BASE}/documents`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function deleteDocument(id: string): Promise<void> {
  const res = await fetch(`${BASE}/documents/${id}`, { method: 'DELETE' })
  if (!res.ok && res.status !== 404) throw new Error(await parseError(res))
}

export async function getDocument(id: string): Promise<Document> {
  const res = await fetch(`${BASE}/documents/${id}`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function getSimilarDocuments(id: string): Promise<SimilarDocument[]> {
  const res = await fetch(`${BASE}/documents/${id}/similar`)
  if (!res.ok) return []
  return res.json()
}

export async function getBOMPreview(id: string): Promise<BOMPreview> {
  const res = await fetch(`${BASE}/documents/${id}/preview`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function recordMatchFeedback(items: MatchFeedback[]): Promise<void> {
  if (items.length === 0) return
  await fetch(`${BASE}/match-feedback`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(items),
  })
}

export async function cloneFromDocument(id: string, sourceId: string): Promise<Document> {
  const res = await fetch(`${BASE}/documents/${id}/bom/clone-from/${sourceId}`, { method: 'POST' })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function saveBOM(id: string, rows: BOMRow[]): Promise<Document> {
  const res = await fetch(`${BASE}/documents/${id}/bom`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(rows),
  })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function saveMapping(
  mapping: Pick<Mapping, 'customerPartNumber' | 'internalPartNumber' | 'manufacturerPartNumber' | 'description' | 'source'>,
): Promise<Mapping> {
  const res = await fetch(`${BASE}/mappings`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(mapping),
  })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function uploadMappingsCSV(file: File): Promise<{ saved: number; skipped: number }> {
  const form = new FormData()
  form.append('file', file)
  const res = await fetch(`${BASE}/mappings/upload`, { method: 'POST', body: form })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function suggestMappings(query: string): Promise<Mapping[]> {
  if (!query.trim()) return []
  const res = await fetch(`${BASE}/mappings/suggest?q=${encodeURIComponent(query)}`)
  if (!res.ok) return []
  return res.json()
}

export function exportCSVUrl(id: string): string {
  return `${BASE}/documents/${id}/bom.csv`
}

export function exportTSVUrl(id: string): string {
  return `${BASE}/documents/${id}/bom.csv?format=tsv`
}

export function exportSAPUrl(id: string): string {
  return `${BASE}/documents/${id}/export/sap`
}

export async function getExportConfig(): Promise<ExportConfig> {
  const res = await fetch(`${BASE}/org/export-config`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function saveExportConfig(cfg: ExportConfig): Promise<ExportConfig> {
  const res = await fetch(`${BASE}/org/export-config`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(cfg),
  })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function checkAuth(): Promise<{ ok: boolean; isAdmin: boolean }> {
  const res = await fetch(`${BASE}/auth/me`)
  if (!res.ok) return { ok: false, isAdmin: false }
  const body = await res.json()
  return { ok: true, isAdmin: body.isAdmin === true }
}

export async function getAdminErrors(): Promise<ErrorLogEntry[]> {
  const res = await fetch(`${BASE}/admin/errors`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function login(username: string, password: string): Promise<void> {
  const res = await fetch(`${BASE}/auth/login`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) throw new Error(await parseError(res))
}

export async function logout(): Promise<void> {
  await fetch(`${BASE}/auth/logout`, { method: 'POST' })
}

export async function createInvite(): Promise<{ token: string; expiresAt: string; inviteUrl: string }> {
  const res = await fetch(`${BASE}/invites`, { method: 'POST' })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function validateInvite(token: string): Promise<{ valid: boolean; orgName: string }> {
  const res = await fetch(`${BASE}/invites/${encodeURIComponent(token)}`)
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function acceptInvite(token: string, username: string, password: string): Promise<void> {
  const res = await fetch(`${BASE}/invites/${encodeURIComponent(token)}/accept`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ username, password }),
  })
  if (!res.ok) throw new Error(await parseError(res))
}

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await fetch(`${BASE}/users/me/password`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newPassword }),
  })
  if (!res.ok) throw new Error(await parseError(res))
}
