// Warden brand mark: a hexagonal watchtower shield enclosing a controlled
// execution pulse shaped like a W (signal crossing the boundary). The beacon
// dot is welded into the top vertex as a deliberate crest so it never reads as
// a stray pixel at the 22px sidebar size; stroke weights are tuned for that.
// Rendered in currentColor to theme with the brand accent on both the graphite
// dark (cyan) and daylight light (amber) surfaces.
export function WardenLogo({ size = 24, className = '' }: { size?: number; className?: string }) {
  return (
    <svg
      width={size}
      height={size}
      viewBox="0 0 32 32"
      fill="none"
      className={className}
      aria-hidden="true"
      focusable="false"
    >
      <path
        d="M16 3.6 L27.4 9.7 V22.3 L16 28.4 L4.6 22.3 V9.7 Z"
        stroke="currentColor"
        strokeWidth="2.4"
        strokeLinejoin="round"
      />
      <path
        d="M9 14 L11.8 21.8 L16 15.6 L20.2 21.8 L23 14"
        stroke="currentColor"
        strokeWidth="2.8"
        strokeLinejoin="round"
        strokeLinecap="round"
      />
      <circle cx="16" cy="5.4" r="2.5" fill="currentColor" />
    </svg>
  )
}
