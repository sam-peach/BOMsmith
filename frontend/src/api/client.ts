import type { BOMRow, Document, Mapping } from '../types/api'

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
  const res = await fetch(`${BASE}/documents/${id}/analyze`, { method: 'POST' })
  if (!res.ok) throw new Error(await parseError(res))
  return res.json()
}

export async function getDocument(id: string): Promise<Document> {
  const res = await fetch(`${BASE}/documents/${id}`)
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

export function exportCSVUrl(id: string): string {
  return `${BASE}/documents/${id}/bom.csv`
}

export async function checkAuth(): Promise<boolean> {
  const res = await fetch(`${BASE}/auth/me`)
  return res.ok
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

export async function changePassword(currentPassword: string, newPassword: string): Promise<void> {
  const res = await fetch(`${BASE}/users/me/password`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ currentPassword, newPassword }),
  })
  if (!res.ok) throw new Error(await parseError(res))
}
