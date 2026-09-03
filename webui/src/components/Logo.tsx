// Cloudpath 标识：云 + 入云之径（单色线条，随 currentColor）
export function Logo({ size = 28 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" fill="none" aria-hidden>
      <path
        d="M22 42h21.5a9.5 9.5 0 0 0 1.9-18.8A13.5 13.5 0 0 0 19.6 25 8.5 8.5 0 0 0 22 42Z"
        stroke="currentColor" strokeWidth="4.5" strokeLinejoin="round"
      />
      <path d="M13 52h38" stroke="currentColor" strokeWidth="4.5" strokeLinecap="round" opacity=".4" />
      <circle cx="32" cy="52" r="3.2" fill="currentColor" />
    </svg>
  )
}