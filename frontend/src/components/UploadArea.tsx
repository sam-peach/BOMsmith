import { useRef, useState } from 'react'
import { colors, radius, shadow } from '../theme'

interface Props {
  onUpload: (file: File) => void
  loading:  boolean
}

export default function UploadArea({ onUpload, loading }: Props) {
  const inputRef = useRef<HTMLInputElement>(null)
  const [dragging, setDragging] = useState(false)

  function handleFiles(files: FileList | null) {
    if (!files || files.length === 0) return
    const file = files[0]
    if (!file.name.toLowerCase().endsWith('.pdf')) {
      alert('Please select a PDF file.')
      return
    }
    onUpload(file)
  }

  return (
    <div
      style={{
        border:       `2px dashed ${dragging ? colors.brand : colors.border}`,
        borderRadius: radius.xl,
        padding:      '72px 40px',
        textAlign:    'center',
        cursor:       loading ? 'wait' : 'pointer',
        background:   dragging ? colors.brandFaint : colors.surface,
        transition:   'border-color 0.15s, background 0.15s',
        opacity:      loading ? 0.65 : 1,
        boxShadow:    shadow.sm,
      }}
      onClick={() => !loading && inputRef.current?.click()}
      onDragOver={e  => { e.preventDefault(); setDragging(true) }}
      onDragLeave={() => setDragging(false)}
      onDrop={e => {
        e.preventDefault()
        setDragging(false)
        if (!loading) handleFiles(e.dataTransfer.files)
      }}
    >
      <input
        ref={inputRef}
        type="file"
        accept=".pdf"
        style={{ display: 'none' }}
        onChange={e => handleFiles(e.target.files)}
      />

      <div style={{ display: 'flex', justifyContent: 'center', marginBottom: 18 }}>
        <UploadIcon active={dragging} />
      </div>

      {loading ? (
        <p style={{ margin: 0, fontWeight: 600, fontSize: 15, color: colors.text }}>
          Uploading…
        </p>
      ) : (
        <>
          <p style={{ margin: '0 0 6px', fontWeight: 600, fontSize: 15, color: colors.text }}>
            Drop a customer drawing here, or click to browse
          </p>
          <p style={{ margin: 0, color: colors.textSubtle, fontSize: 13 }}>
            PDF files only · max 32 MB
          </p>
        </>
      )}
    </div>
  )
}

function UploadIcon({ active = false }: { active?: boolean }) {
  const c = active ? colors.brand : '#b8b3bf'
  return (
    <svg width="52" height="52" viewBox="0 0 52 52" fill="none" aria-hidden="true">
      {/* Outer ring */}
      <circle cx="26" cy="26" r="22" stroke={c} strokeWidth="1.5" opacity="0.2" />
      {/* Up arrow shaft */}
      <path d="M26 34V20" stroke={c} strokeWidth="2" strokeLinecap="round" />
      {/* Up arrow head */}
      <path d="M19 27l7-8 7 8" stroke={c} strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
      {/* Base line */}
      <path d="M18 37h16" stroke={c} strokeWidth="1.5" strokeLinecap="round" />
    </svg>
  )
}
